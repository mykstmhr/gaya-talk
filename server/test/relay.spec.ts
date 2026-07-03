import { SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";

const TOKEN_RE = /^[A-Za-z0-9_-]{22}$/;

async function createToken(): Promise<string> {
  const res = await SELF.fetch("https://example.com/rooms", { method: "POST" });
  expect(res.status).toBe(200);
  const body = (await res.json()) as { token: string };
  return body.token;
}

async function openWs(token: string): Promise<{ ws: WebSocket; received: string[] }> {
  const res = await SELF.fetch(`https://example.com/r/${token}/ws`, {
    headers: { Upgrade: "websocket" },
  });
  expect(res.status).toBe(101);
  const ws = res.webSocket!;
  const received: string[] = [];
  ws.addEventListener("message", (ev) => {
    received.push(ev.data as string);
  });
  ws.accept();
  return { ws, received };
}

async function waitFor(cond: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) throw new Error("waitFor timeout");
    await new Promise((r) => setTimeout(r, 10));
  }
}

describe("POST /rooms", () => {
  it("22 文字の base64url トークンを返す", async () => {
    const token = await createToken();
    expect(token).toMatch(TOKEN_RE);
    // 毎回ランダムに生成されること
    expect(await createToken()).not.toBe(token);
  });
});

describe("GET /r/<token>/ws", () => {
  it("不正な token は 404", async () => {
    for (const path of [
      "/r/short/ws",
      "/r/AAAAAAAAAAAAAAAAAAAAAAA/ws", // 23 文字
      "/r/AAAAAAAAAAAAAAAAAAAA+A/ws", // base64url 外の文字
      "/unknown",
    ]) {
      const res = await SELF.fetch(`https://example.com${path}`, {
        headers: { Upgrade: "websocket" },
      });
      expect(res.status).toBe(404);
    }
  });
});

describe("Room ブロードキャスト", () => {
  it("送信者自身を含む全参加者にメッセージが届く", async () => {
    const token = await createToken();
    const a = await openWs(token);
    const b = await openWs(token);

    a.ws.send('{"v":1,"body":"encrypted"}');

    await waitFor(() => a.received.length >= 1 && b.received.length >= 1);
    expect(a.received).toEqual(['{"v":1,"body":"encrypted"}']);
    expect(b.received).toEqual(['{"v":1,"body":"encrypted"}']);
  });

  it("レートリミット(10 秒 30 通)超過分は転送されない", async () => {
    const token = await createToken();
    const sender = await openWs(token);
    const receiver = await openWs(token);

    for (let i = 0; i < 35; i++) {
      sender.ws.send(`msg-${i}`);
    }
    await waitFor(() => receiver.received.length >= 30);

    // 別接続のレートリミットは独立なので marker は通る。
    // marker 到着をもって sender 分の配送完了を確定させる
    sender.ws.send("should-be-dropped");
    receiver.ws.send("marker");
    await waitFor(() => receiver.received.includes("marker"));

    const fromSender = receiver.received.filter((m) => m.startsWith("msg-") || m === "should-be-dropped");
    expect(fromSender).toEqual(Array.from({ length: 30 }, (_, i) => `msg-${i}`));
  });
});
