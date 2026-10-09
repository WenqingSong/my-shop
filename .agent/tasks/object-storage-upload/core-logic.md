# Owner Core Logic 验证卡

本文件承载值得 Owner 亲自理解与验证的核心机制。Owner 决策状态由 OwnerGate 在 Owner 明确决定后持久化，本文件不承担决策权威。

## CL-001：上传凭证签发核心（key 唯一可控 + token scope 固化文件边界）

- Owner 需要理解：后端为每次签发预生成唯一存储 key 并写入 token 的 `scope=bucket:key`，同时把 MIME/大小白名单固化进 token（`mimeLimit`/`fsizeLimit`）。若 scope 退化为 `bucket`（不绑定 key）或白名单未固化，客户端可覆盖他人对象或上传任意类型/大小文件，造成数据越界与污染。
- 生产代码：`internal/logic/upload/upload.go` 的 `IssueToken`（`generateKey` 生成 `upload/{yyyyMMdd}/{hex32}.{ext}`；`PutPolicy{Scope: bucket+":"+key, MimeLimit, FsizeLimit}`）；`validateExtension`/`validateContentType` 白名单校验。
- 关键测试：`internal/cmd/upload_test.go` `TestUploadTokenHappyPath`（断言 scope=bucket:key、mimeLimit/fsizeLimit 与配置一致、连续签发 key 不同）、`TestUploadTokenInvalidInput`（非法扩展名/MIME → 400/17001）。
- 基线验证：`go test -p 1 ./internal/cmd/ -run 'TestUploadTokenHappyPath|TestUploadTokenInvalidInput' -v` 预期全部 PASS。
- 可选 Mutation：把 `IssueToken` 中 `Scope: cfg.bucket + ":" + key` 改为 `Scope: cfg.bucket`。
- 预期失败：`TestUploadTokenHappyPath` 断言 `scope == "test-bucket:"+d.Key` 将失败（scope 变为 `test-bucket`）。
- 恢复确认：还原 scope 后再次运行 `go test -p 1 ./internal/cmd/ -run TestUploadTokenHappyPath` 应恢复 PASS。

## CL-002：启动 fail-fast + 最小权限只读可用性检查

- Owner 需要理解：正常 `serve` 启动时，七牛为 required dependency——配置缺失/非法或对指定 bucket 的 `GetBucketInfo`（最小权限只读，非 `Buckets()` 全账户列举）真实检查失败即进程非零退出；`17002` 仅作运行期防御性守卫。若校验被弱化（如漏掉 domain、改用 `Buckets()`、或改为懒校验），服务会在无凭据/无权限时照常启动，直到签发时才暴露问题。
- 生产代码：`internal/logic/upload/upload.go` `ValidateConfig`（`validateStructural` → `validateRequired` → `checkBucketAvailable`，其中 `checkBucketAvailable` 用 `BucketManager.GetBucketInfo(bucket)` + 带 10s Timeout 的 http.Client）；`internal/cmd/cmd.go` `serve()` 在 `boot.Bootstrap` 后调用 `service.Upload().ValidateConfig` 失败即返回错误 → 非零退出。
- 关键测试：`internal/logic/upload/upload_test.go` `TestValidateRequired`（缺 AK/SK/bucket/domain 任一失败且错误指认字段名）、`TestValidateConfigAvailabilityWiring`（注入假检查函数验证接线）；`internal/cmd/upload_test.go` `TestUploadTokenMissingConfig`（运行期无凭据 → 500/17002）。
- 基线验证：`go test -p 1 ./internal/logic/upload/ -v` 预期全部 PASS；`./bin/my-shop qiniu check`（无凭据）预期 exit 1 且输出 `qiniu.access_key 未配置`。
- 可选 Mutation：删除 `validateRequired` 中的 `case c.domain == "":` 分支（不再校验 domain）。
- 预期失败：`TestValidateRequired` 的「缺 domain」子用例将失败（缺失字段不再被拒绝）。
- 恢复确认：还原 domain 校验后再次运行 `go test -p 1 ./internal/logic/upload/ -run TestValidateRequired` 应恢复 PASS。
