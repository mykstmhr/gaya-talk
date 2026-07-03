// vitest-pool-workers の cloudflare:test が返す env に wrangler.jsonc の
// バインディング(ROOM など)の型を付ける。
import type { Env } from "../src/index";

declare module "cloudflare:test" {
  interface ProvidedEnv extends Env {}
}
