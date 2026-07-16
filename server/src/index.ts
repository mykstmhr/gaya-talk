// gaya-talk room リレーサーバ。
// E2E 暗号化済みメッセージを同一ルームの全参加者へそのまま中継するだけで、
// 本文の復号も保存も一切しない。DO の storage に置くのはルームのメタデータ
// (管理シークレットのハッシュとタイムスタンプ)だけ。
import { DurableObject } from "cloudflare:workers";

export interface Env {
  ROOM: DurableObjectNamespace<Room>;
  /** 未アクティブなルームが失効するまでの日数(wrangler.jsonc の vars。既定 7) */
  ROOM_IDLE_TTL_DAYS?: string;
  /**
   * 設定すると POST /rooms に Authorization: Bearer <この値> を要求する
   * (`wrangler secret put CREATE_SECRET` で設定)。サーバ URL は招待 URL の一部として
   * 全参加者に見えるため、これが無いと URL を知る誰でもルームを作り放題になる。
   * 未設定なら従来どおり無認証(自分しか URL を知らない個人運用向け)。
   */
  CREATE_SECRET?: string;
}

// 128bit トークンの base64url(パディングなし)表現 = 22 文字
const TOKEN = "([A-Za-z0-9_-]{22})";
const WS_PATH_RE = new RegExp(`^/r/${TOKEN}/ws$`);
const ROOM_PATH_RE = new RegExp(`^/r/${TOKEN}$`);

const MAX_MESSAGE_BYTES = 16 * 1024;
const RATE_LIMIT_WINDOW_MS = 10_000;
const RATE_LIMIT_MAX_MESSAGES = 30;
// 1 ルームの同時接続数の上限。ブロードキャストは O(接続数) なので、トークン保持者が
// 接続を大量に張って DO の CPU・課金を増幅させるのを防ぐ(正規の用途には十分な数)
const MAX_CONNECTIONS_PER_ROOM = 32;

const DEFAULT_IDLE_TTL_DAYS = 7;
// メッセージ起因の lastActive 書き込みはこの間隔まで間引く(接続時は毎回書く)
const ACTIVITY_WRITE_INTERVAL_MS = 60 * 60 * 1000;
// クライアントは 30 秒ごとに心拍("ping" テキスト)を送る。これがこの時間途絶えた
// ソケットは接続数に数えず閉じる(sleep した Mac の接続は TCP 上なにも言わずに
// 消えるため、エッジには「開いたまま」でしばらく残る)。3 拍ぶんの余裕を持たせる
const HEARTBEAT_STALE_MS = 90_000;

function generateToken(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

const ENC = new TextEncoder();

async function sha256Hex(s: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", ENC.encode(s));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === "POST" && url.pathname === "/rooms") {
      if (env.CREATE_SECRET) {
        const auth = request.headers.get("Authorization") ?? "";
        const secret = auth.startsWith("Bearer ") ? auth.slice("Bearer ".length) : "";
        // ハッシュ同士を比較して、文字列比較の打ち切りタイミングから秘密が漏れないようにする
        if (secret === "" || (await sha256Hex(secret)) !== (await sha256Hex(env.CREATE_SECRET))) {
          return new Response("Unauthorized", { status: 401 });
        }
      }
      // トークンと管理シークレットを発行し、DO を初期化して有効化する。
      // 初期化されていないトークンは接続を受け付けないので、適当な 22 文字を
      // 並べてリレーにタダ乗りすることはできない
      const token = generateToken();
      const adminSecret = generateToken();
      const stub = env.ROOM.get(env.ROOM.idFromName(token));
      await stub.init(await sha256Hex(adminSecret));
      return Response.json({ token, adminSecret });
    }

    const wsMatch = url.pathname.match(WS_PATH_RE);
    if (request.method === "GET" && wsMatch) {
      const stub = env.ROOM.get(env.ROOM.idFromName(wsMatch[1]));
      return stub.fetch(request);
    }

    // ルームの無効化(DELETE)と接続数の取得(GET)。どちらも作成者のみ
    // (Authorization: Bearer <adminSecret>)
    const roomMatch = url.pathname.match(ROOM_PATH_RE);
    if ((request.method === "DELETE" || request.method === "GET") && roomMatch) {
      const stub = env.ROOM.get(env.ROOM.idFromName(roomMatch[1]));
      return stub.fetch(request);
    }

    return new Response("Not Found", { status: 404 });
  },
} satisfies ExportedHandler<Env>;

// 接続ごとのレートリミット状態。ハイバネーション後もソケットの
// attachment として復元されるよう、in-memory ではなくここに持たせる
interface Attachment {
  /** 直近ウィンドウ内に受理したメッセージの epoch ms */
  recv: number[];
}

// ルームのメタデータ(storage の "meta" キー)。本文は一切含まない。
// revoked を立てたら消さずに残す(墓標)。deleteAll でルームごと消すと、
// 次の接続で DO がまっさらに再生成されて URL が復活してしまうため
interface Meta {
  /** 管理シークレットの SHA-256(hex)。シークレット自体は保存しない */
  adminHash: string;
  createdAt: number;
  /** 最後に接続 or 発言があった epoch ms(失効判定に使う) */
  lastActive: number;
  /** 無効化済み(管理者操作 or 失効)。以後の接続は 410 */
  revoked?: boolean;
}

export class Room extends DurableObject<Env> {
  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    // クライアントの心拍("ping")にはエッジが DO を起こさず "pong" を返す。
    // auto-response に食われるので webSocketMessage には届かず、中継も
    // レートリミット消費もされない(= 心拍のコストは実質ゼロ)。
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
  }

  // POST /rooms から RPC で呼ばれ、ルームを有効化する。
  // 既に初期化済みなら何もしない(128bit 乱数の衝突は実質起きないが、
  // 万一のとき既存ルームの管理者を上書きさせないため)
  async init(adminHash: string): Promise<void> {
    const meta = await this.ctx.storage.get<Meta>("meta");
    if (meta) return;
    const now = Date.now();
    await this.ctx.storage.put("meta", { adminHash, createdAt: now, lastActive: now } satisfies Meta);
  }

  private idleTtlMs(): number {
    const days = Number(this.env.ROOM_IDLE_TTL_DAYS ?? "");
    return (Number.isFinite(days) && days > 0 ? days : DEFAULT_IDLE_TTL_DAYS) * 24 * 60 * 60 * 1000;
  }

  async fetch(request: Request): Promise<Response> {
    if (request.method === "DELETE") {
      return this.revoke(request);
    }

    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      // Upgrade の無い GET は接続数の取得。ブラウザで共有 URL を開いただけの
      // アクセスもここに来るが、管理シークレットが無ければ 403 で終わる
      if (request.method === "GET") {
        return this.status(request);
      }
      return new Response("Expected WebSocket", { status: 426 });
    }

    const meta = await this.ctx.storage.get<Meta>("meta");
    if (!meta) {
      // POST /rooms を経ていないトークン(または旧サーバ時代の URL)
      return new Response("Room not found", { status: 404 });
    }
    const now = Date.now();
    if (meta.revoked) {
      return new Response("Room revoked or expired", { status: 410 });
    }
    if (now - meta.lastActive > this.idleTtlMs()) {
      // 失効。墓標を立て、繋ぎっぱなしのソケットも道連れに閉じる
      await this.markRevoked(meta);
      return new Response("Room revoked or expired", { status: 410 });
    }
    if (this.ctx.getWebSockets().length >= MAX_CONNECTIONS_PER_ROOM) {
      return new Response("Too many connections", { status: 503 });
    }
    await this.ctx.storage.put("meta", { ...meta, lastActive: now } satisfies Meta);

    const pair = new WebSocketPair();
    const [client, server] = [pair[0], pair[1]];
    // Hibernation API: accept ではなく acceptWebSocket を使うことで
    // アイドル時に DO をメモリから退避でき、無料枠で運用できる
    this.ctx.acceptWebSocket(server);
    server.serializeAttachment({ recv: [] } satisfies Attachment);

    return new Response(null, { status: 101, webSocket: client });
  }

  // 管理シークレット(Authorization: Bearer)が meta のハッシュと一致するか。
  private async authorized(request: Request, meta: Meta): Promise<boolean> {
    const auth = request.headers.get("Authorization") ?? "";
    const secret = auth.startsWith("Bearer ") ? auth.slice("Bearer ".length) : "";
    return secret !== "" && (await sha256Hex(secret)) === meta.adminHash;
  }

  // ルームを無効化し、接続中の全ソケットを閉じる。
  private async revoke(request: Request): Promise<Response> {
    const meta = await this.ctx.storage.get<Meta>("meta");
    if (!meta) {
      return new Response("Room not found", { status: 404 });
    }
    if (!(await this.authorized(request, meta))) {
      return new Response("Forbidden", { status: 403 });
    }
    await this.markRevoked(meta);
    return new Response(null, { status: 204 });
  }

  // 現在の同時接続数を返す(作成者のみ)。接続数はサーバが中継のために元々
  // 持っているメタデータなので、これを出しても本文の秘匿性(E2E)は変わらない。
  private async status(request: Request): Promise<Response> {
    const meta = await this.ctx.storage.get<Meta>("meta");
    if (!meta) {
      return new Response("Room not found", { status: 404 });
    }
    if (!(await this.authorized(request, meta))) {
      return new Response("Forbidden", { status: 403 });
    }
    if (meta.revoked) {
      return new Response("Room revoked or expired", { status: 410 });
    }
    // 心拍(auto-response の最終応答時刻)が新しいソケットだけ数える。
    // timestamp が null なのは接続直後(初回 ping 前)か旧クライアントで、
    // 生死を判定できないので数に入れる。getWebSockets() はハイバネーション中
    // の接続も含めて列挙する
    const now = Date.now();
    let connections = 0;
    for (const ws of this.ctx.getWebSockets()) {
      const seen = this.ctx.getWebSocketAutoResponseTimestamp(ws);
      if (seen && now - seen.getTime() > HEARTBEAT_STALE_MS) {
        // ついでに掃除する(sleep したまま残ったゾンビが接続上限を食い潰さないように)
        try {
          ws.close(1001, "heartbeat timeout");
        } catch {
          // すでに閉じている場合は無視
        }
        continue;
      }
      connections++;
    }
    return Response.json({ connections });
  }

  // 墓標(revoked)を立て、接続中の全ソケットを閉じる。管理者による無効化と
  // TTL 失効の両方から呼ぶ(片方だけだと既存接続が中継し続けてしまう)。
  private async markRevoked(meta: Meta): Promise<void> {
    await this.ctx.storage.put("meta", { ...meta, revoked: true } satisfies Meta);
    for (const ws of this.ctx.getWebSockets()) {
      try {
        ws.close(1008, "room revoked");
      } catch {
        // すでに閉じている場合は無視
      }
    }
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (typeof message !== "string") return; // バイナリは無視
    if (ENC.encode(message).byteLength > MAX_MESSAGE_BYTES) return;

    const now = Date.now();
    // 中継前に墓標と失効を確認する。これが無いと、無効化・失効の後も
    // 繋ぎっぱなしのソケット同士は永久に中継できてしまう
    // (storage はメモリキャッシュされるので hot path でも安価)
    const meta = await this.ctx.storage.get<Meta>("meta");
    if (!meta || meta.revoked) {
      try {
        ws.close(1008, "room revoked");
      } catch {
        // すでに閉じている場合は無視
      }
      return;
    }
    if (now - meta.lastActive > this.idleTtlMs()) {
      await this.markRevoked(meta);
      return;
    }

    const attachment = (ws.deserializeAttachment() as Attachment | null) ?? { recv: [] };
    const recv = attachment.recv.filter((t) => now - t < RATE_LIMIT_WINDOW_MS);
    if (recv.length >= RATE_LIMIT_MAX_MESSAGES) return; // 超過分は黙って捨てる
    recv.push(now);
    ws.serializeAttachment({ recv } satisfies Attachment);

    // 送信者自身を含む全接続へブロードキャスト。
    // getWebSockets() はハイバネーション中の接続も含めて列挙する
    for (const peer of this.ctx.getWebSockets()) {
      try {
        peer.send(message);
      } catch {
        // クローズ処理中のソケットへの send 失敗は他の参加者に影響させない
      }
    }

    // 繋ぎっぱなしの常設ルームが「接続時刻だけ古い」せいで失効しないよう、
    // 発言でも lastActive を進める(書き込みは間引く)
    if (now - meta.lastActive > ACTIVITY_WRITE_INTERVAL_MS) {
      await this.ctx.storage.put("meta", { ...meta, lastActive: now } satisfies Meta);
    }
  }

  async webSocketClose(ws: WebSocket, code: number): Promise<void> {
    try {
      ws.close(code, "room closing");
    } catch {
      // すでに閉じている場合は無視
    }
  }

  async webSocketError(ws: WebSocket): Promise<void> {
    try {
      ws.close(1011, "websocket error");
    } catch {
      // すでに閉じている場合は無視
    }
  }
}
