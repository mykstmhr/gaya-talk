// ura-talk room リレーサーバ。
// E2E 暗号化済みメッセージを同一ルームの全参加者へそのまま中継するだけで、
// 本文の復号も永続化も一切しない(state.storage には書き込まない)。
import { DurableObject } from "cloudflare:workers";

export interface Env {
  ROOM: DurableObjectNamespace<Room>;
}

// 128bit トークンの base64url(パディングなし)表現 = 22 文字
const WS_PATH_RE = /^\/r\/([A-Za-z0-9_-]{22})\/ws$/;

const MAX_MESSAGE_BYTES = 16 * 1024;
const RATE_LIMIT_WINDOW_MS = 10_000;
const RATE_LIMIT_MAX_MESSAGES = 30;

function generateToken(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === "POST" && url.pathname === "/rooms") {
      // サーバ側の登録処理は不要。DO は初回 WebSocket 接続時に暗黙に作られる
      return Response.json({ token: generateToken() });
    }

    const match = url.pathname.match(WS_PATH_RE);
    if (request.method === "GET" && match) {
      const stub = env.ROOM.get(env.ROOM.idFromName(match[1]));
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

export class Room extends DurableObject<Env> {
  async fetch(request: Request): Promise<Response> {
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return new Response("Expected WebSocket", { status: 426 });
    }

    const pair = new WebSocketPair();
    const [client, server] = [pair[0], pair[1]];
    // Hibernation API: accept ではなく acceptWebSocket を使うことで
    // アイドル時に DO をメモリから退避でき、無料枠で運用できる
    this.ctx.acceptWebSocket(server);
    server.serializeAttachment({ recv: [] } satisfies Attachment);

    return new Response(null, { status: 101, webSocket: client });
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
