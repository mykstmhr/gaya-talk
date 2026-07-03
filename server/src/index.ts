// ura-talk room リレーサーバ。
// E2E 暗号化済みメッセージを同一ルームの全参加者へそのまま中継するだけで、
// 本文の復号も保存も一切しない。DO の storage に置くのはルームのメタデータ
// (管理シークレットのハッシュとタイムスタンプ)だけ。
import { DurableObject } from "cloudflare:workers";

export interface Env {
  ROOM: DurableObjectNamespace<Room>;
  /** 未アクティブなルームが失効するまでの日数(wrangler.jsonc の vars。既定 7) */
  ROOM_IDLE_TTL_DAYS?: string;
}

// 128bit トークンの base64url(パディングなし)表現 = 22 文字
const TOKEN = "([A-Za-z0-9_-]{22})";
const WS_PATH_RE = new RegExp(`^/r/${TOKEN}/ws$`);
const ROOM_PATH_RE = new RegExp(`^/r/${TOKEN}$`);

const MAX_MESSAGE_BYTES = 16 * 1024;
const RATE_LIMIT_WINDOW_MS = 10_000;
const RATE_LIMIT_MAX_MESSAGES = 30;

const DEFAULT_IDLE_TTL_DAYS = 7;
// メッセージ起因の lastActive 書き込みはこの間隔まで間引く(接続時は毎回書く)
const ACTIVITY_WRITE_INTERVAL_MS = 60 * 60 * 1000;

function generateToken(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

async function sha256Hex(s: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === "POST" && url.pathname === "/rooms") {
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

    // ルームの無効化(作成者のみ)。Authorization: Bearer <adminSecret>
    const roomMatch = url.pathname.match(ROOM_PATH_RE);
    if (request.method === "DELETE" && roomMatch) {
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
      // 失効。墓標として revoked を立てて残す
      await this.ctx.storage.put("meta", { ...meta, revoked: true } satisfies Meta);
      return new Response("Room revoked or expired", { status: 410 });
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

  // ルームを無効化し、接続中の全ソケットを閉じる。
  private async revoke(request: Request): Promise<Response> {
    const meta = await this.ctx.storage.get<Meta>("meta");
    if (!meta) {
      return new Response("Room not found", { status: 404 });
    }
    const auth = request.headers.get("Authorization") ?? "";
    const secret = auth.startsWith("Bearer ") ? auth.slice("Bearer ".length) : "";
    if (secret === "" || (await sha256Hex(secret)) !== meta.adminHash) {
      return new Response("Forbidden", { status: 403 });
    }
    await this.ctx.storage.put("meta", { ...meta, revoked: true } satisfies Meta);
    for (const ws of this.ctx.getWebSockets()) {
      try {
        ws.close(1008, "room revoked");
      } catch {
        // すでに閉じている場合は無視
      }
    }
    return new Response(null, { status: 204 });
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (typeof message !== "string") return; // バイナリは無視
    if (new TextEncoder().encode(message).byteLength > MAX_MESSAGE_BYTES) return;

    const now = Date.now();
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
    const meta = await this.ctx.storage.get<Meta>("meta");
    if (meta && !meta.revoked && now - meta.lastActive > ACTIVITY_WRITE_INTERVAL_MS) {
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
