// vitest v4 + @cloudflare/vitest-pool-workers 0.18 以降はプラグイン形式で設定する
// (旧 defineWorkersConfig / poolOptions 形式は廃止された)。
import { defineConfig } from "vitest/config";
import { cloudflareTest } from "@cloudflare/vitest-pool-workers";

export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.jsonc" },
    }),
  ],
});
