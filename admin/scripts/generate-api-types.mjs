#!/usr/bin/env node
/**
 * 从后端 OpenAPI 契约生成 TypeScript 类型。
 *
 * 契约位置：仓库根目录 `contracts/openapi.yaml`（由后端 B0 阶段交付）。
 * 生成结果：`src/api/generated/schema.d.ts`（不手工编辑）。
 *
 * 契约尚未交付时会明确报错并给出说明，而不是产生空文件或伪造类型。
 * 可通过环境变量 `OPENAPI_SPEC` 指定其他契约路径。
 */
import { existsSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const adminRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const spec = process.env.OPENAPI_SPEC
  ? resolve(process.cwd(), process.env.OPENAPI_SPEC)
  : resolve(adminRoot, "../contracts/openapi.yaml");
const outFile = resolve(adminRoot, "src/api/generated/schema.d.ts");

if (!existsSync(spec)) {
  console.error("");
  console.error("[api:types] 未找到 OpenAPI 契约文件：");
  console.error(`  ${spec}`);
  console.error("");
  console.error(
    "该契约由后端在 B0 阶段交付到仓库根目录 contracts/openapi.yaml。"
  );
  console.error(
    "在契约交付前，本脚本不会生成任何类型（避免与真实契约不一致）。"
  );
  console.error(
    "交付后重新执行 `pnpm api:types` 即可生成 src/api/generated/schema.d.ts。"
  );
  console.error("");
  process.exit(1);
}

mkdirSync(dirname(outFile), { recursive: true });

const result = spawnSync(
  process.execPath,
  [
    resolve(adminRoot, "node_modules/openapi-typescript/bin/cli.js"),
    spec,
    "-o",
    outFile
  ],
  { stdio: "inherit", cwd: adminRoot }
);

if (result.status === 0) {
  console.log(`[api:types] 类型已生成：${outFile}`);
}
process.exit(result.status ?? 1);
