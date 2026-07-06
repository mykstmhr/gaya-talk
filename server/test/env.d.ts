// vitest-pool-workers の cloudflare:test が返す env は Cloudflare.Env 型
// (workers-types が宣言する空インターフェース)なので、wrangler.jsonc の
// バインディング(ROOM など)の型を declaration merging で足す。
import type { Env as WorkerEnv } from "../src/index";

declare global {
  namespace Cloudflare {
    interface Env extends WorkerEnv {}
  }
}
