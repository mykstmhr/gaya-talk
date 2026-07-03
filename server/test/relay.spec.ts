import { SELF, env, runInDurableObject } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import type { Room } from "../src/index";

const TOKEN_RE = /^[A-Za-z0-9_-]{22}$/;

interface Created {
  token: string;
  adminSecret: string;
}

async function createRoom(): Promise<Created> {
  const res = await SELF.fetch("https://example.com/rooms", { method: "POST" });
  expect(res.status).toBe(200);
  return (await res.json()) as Created;
}

async function openWs(token: string): Promise<{ ws: WebSocket; received: string[]; closed: () => boolean }> {
  const res = await SELF.fetch(`https://example.com/r/${token}/ws`, {
    headers: { Upgrade: "websocket" },
  });
  expect(res.status).toBe(101);
  const ws = res.webSocket!;
  const received: string[] = [];
  let closed = false;
  ws.addEventListener("message", (ev) => {
    received.push(ev.data as string);
  });
  ws.addEventListener("close", () => {
    closed = true;
  });
  ws.accept();
  return { ws, received, closed: () => closed };
}

async function wsStatus(token: string): Promise<number> {
  const res = await SELF.fetch(`https://example.com/r/${token}/ws`, {
    headers: { Upgrade: "websocket" },
  });
  return res.status;
}

// DO の storage 上のメタデータを読み書きする(失効テスト用の時刻巻き戻しなど)。
interface Meta {
  adminHash: string;
  createdAt: number;
  lastActive: number;
  revoked?: boolean;
}

async function getMeta(token: string): Promise<Meta | undefined> {
  const stub = env.ROOM.get(env.ROOM.idFromName(token));
  return runInDurableObject(stub, (_instance: Room, state) => state.storage.get<Meta>("meta"));
}

async function patchMeta(token: string, patch: Partial<Meta>): Promise<void> {
  const stub = env.ROOM.get(env.ROOM.idFromName(token));
  await runInDurableObject(stub, async (_instance: Room, state) => {
    const meta = await state.storage.get<Meta>("meta");
    if (!meta) throw new Error("meta がありません");
    await state.storage.put("meta", { ...meta, ...patch });
  });
}

async function waitFor(cond: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) throw new Error("waitFor timeout");
    await new Promise((r) => setTimeout(r, 10));
  }
}

const DAY_MS = 24 * 60 * 60 * 1000;

describe("POST /rooms", () => {
  it("22 文字の base64url トークンと管理シークレットを返す", async () => {
    const a = await createRoom();
    expect(a.token).toMatch(TOKEN_RE);
    expect(a.adminSecret).toMatch(TOKEN_RE);
    // 毎回ランダムに生成されること
    const b = await createRoom();
    expect(b.token).not.toBe(a.token);
    expect(b.adminSecret).not.toBe(a.adminSecret);
  });

  it("DO を初期化する(シークレット自体は保存せずハッシュだけ)", async () => {
    const { token, adminSecret } = await createRoom();
    const meta = await getMeta(token);
    expect(meta).toBeDefined();
    expect(meta!.adminHash).toMatch(/^[0-9a-f]{64}$/);
    expect(meta!.adminHash).not.toContain(adminSecret);
    expect(meta!.createdAt).toBeGreaterThan(0);
    expect(meta!.lastActive).toBe(meta!.createdAt);
    expect(meta!.revoked).toBeUndefined();
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

  it("POST /rooms を経ていない token は形式が正しくても 404(タダ乗り防止)", async () => {
    expect(await wsStatus("AAAAAAAAAAAAAAAAAAAAAA")).toBe(404);
  });

  it("接続すると lastActive が進む", async () => {
    const { token } = await createRoom();
    await patchMeta(token, { lastActive: Date.now() - 3 * DAY_MS });
    const { ws } = await openWs(token); // TTL(7日)内なので接続できる
    const meta = await getMeta(token);
    expect(Date.now() - meta!.lastActive).toBeLessThan(60_000);
    ws.close();
  });
});

describe("ルームの失効(未アクティブ TTL)", () => {
  it("TTL を超えて未アクティブなら 410 になり、以後もずっと 410(墓標)", async () => {
    const { token } = await createRoom();
    await patchMeta(token, { lastActive: Date.now() - 8 * DAY_MS });

    expect(await wsStatus(token)).toBe(410);
    // 失効後の再接続でルームが復活しないこと
    expect(await wsStatus(token)).toBe(410);
    const meta = await getMeta(token);
    expect(meta!.revoked).toBe(true);
  });

  it("TTL 内のルームは接続できる", async () => {
    const { token } = await createRoom();
    await patchMeta(token, { lastActive: Date.now() - 6 * DAY_MS });
    expect(await wsStatus(token)).toBe(101);
  });
});

describe("DELETE /r/<token>(無効化)", () => {
  it("管理シークレットが一致すれば 204 で無効化し、接続中のソケットも閉じる", async () => {
    const { token, adminSecret } = await createRoom();
    const a = await openWs(token);

    const res = await SELF.fetch(`https://example.com/r/${token}`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${adminSecret}` },
    });
    expect(res.status).toBe(204);

    await waitFor(() => a.closed());
    expect(await wsStatus(token)).toBe(410);
  });

  it("シークレット不一致・欠落は 403 で、ルームは生きたまま", async () => {
    const { token } = await createRoom();
    const wrong = await SELF.fetch(`https://example.com/r/${token}`, {
      method: "DELETE",
      headers: { Authorization: "Bearer AAAAAAAAAAAAAAAAAAAAAA" },
    });
    expect(wrong.status).toBe(403);
    const missing = await SELF.fetch(`https://example.com/r/${token}`, { method: "DELETE" });
    expect(missing.status).toBe(403);
    expect(await wsStatus(token)).toBe(101);
  });

  it("存在しないルームは 404", async () => {
    const res = await SELF.fetch("https://example.com/r/AAAAAAAAAAAAAAAAAAAAAA", {
      method: "DELETE",
      headers: { Authorization: "Bearer AAAAAAAAAAAAAAAAAAAAAA" },
    });
    expect(res.status).toBe(404);
  });
});

describe("Room ブロードキャスト", () => {
  it("送信者自身を含む全参加者にメッセージが届く", async () => {
    const { token } = await createRoom();
    const a = await openWs(token);
    const b = await openWs(token);

    a.ws.send('{"v":1,"body":"encrypted"}');

    await waitFor(() => a.received.length >= 1 && b.received.length >= 1);
    expect(a.received).toEqual(['{"v":1,"body":"encrypted"}']);
    expect(b.received).toEqual(['{"v":1,"body":"encrypted"}']);
  });

  it("レートリミット(10 秒 30 通)超過分は転送されない", async () => {
    const { token } = await createRoom();
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

  it("発言すると lastActive が進む(1 時間以上空いていた場合)", async () => {
    const { token } = await createRoom();
    const a = await openWs(token);
    await patchMeta(token, { lastActive: Date.now() - 2 * 60 * 60 * 1000 });

    a.ws.send("hello");
    await waitFor(() => a.received.length >= 1);

    const meta = await getMeta(token);
    expect(Date.now() - meta!.lastActive).toBeLessThan(60_000);
  });
});
