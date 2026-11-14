# 手动草稿持久恢复（2026-11-11 下一集中批）

## 范围与设计依据

承接 F–O 固定候选 `780745fc55d71edb22e46d9c3e4f208d5ecb602f` 的 O06/O11、编辑草稿与退出/重开要求。本批是 004 之后的源码实现，不属于正在运行的 004 包；尚无本批 Native 窗口或完整包验收。

手动新建与编辑只保存客户端草稿和原保存请求，不自动转成有效知识；delete/move/restore 不接受为可恢复意图。原正文版本变化保留旧 revision 基线，继续使用实际冲突流程，不丢文本或擅自覆盖。

## 持久性与身份

复用服务 MaterialPreview GET 返回的真实库 identity。每个 identity 独立文件及非阻塞排他文件锁，单 writer 串行保存；私有目录、0600 文件、O_NOFOLLOW/openat、有界 UTF-8/严格 JSON、未知和重复字段拒绝，临时文件 fsync、原子替换后目录 fsync。无 schema 迁移。

保留原 dataset ID 和显示标签快照。项目更名或原标签被另一个项目复用不会重定目标；缺失目标保留草稿并明确原范围不可用。同名前缀选项使用完整 ID 去歧义；仅明确选择才改变新草稿目标。未确认保存请求不能借范围选择扩大原请求。

退出依次等待经验、整理和手动草稿 barrier；保存有效知识前先持久化原 operation ID 和请求。写失败/unknown 保留草稿与原请求，阻止 Apply 或退出；重试先重放完全相同的 unknown 本地写，再持久最新文本。已保存知识仍须将移除草稿的空文件 tombstone 确认持久后退出。

纯加载失败且从未读入、修改或排队任何草稿/请求，不取得文件所有权，可退出并保持原文件字节不变；已拥有草稿后的错误不能走此例外。已核验 identity 后停服，本地草稿 flush 不依赖 daemon。冷启动时服务已停则不信旧 epoch 缓存、不读取旧文件，提示明确启动服务核验后恢复。

整库恢复或 ConfigureLibrary 退休旧 writer、清旧内存草稿，旧 writer 只能访问旧 epoch 文件。读取同步冻结 client/lifetime/dispatcher/epoch；跨库迟到回执拒绝，草稿恢复另退休此前页面请求，避免晚到列表覆盖新输入。

## 验证记录

- `91685`：`sh scripts/go.sh test ./internal/desktop -run '^(TestManualDraft|TestLibraryReadFreezes|TestProjectRegistrationReceipt)' -count=1`，Go 输出通过（1.331s）。覆盖真实 SQLite + Unix IPC 已提交但回执丢失后的同 operation 重放，最终仅一条知识/revision 1；真实子进程重开私有草稿文件；版本基线、同名范围、离线 barrier、unknown 写、tombstone、损坏/锁占用退出、危险文件拒绝和旧读取/注册回执隔离。
- `98486`：上述定向 `-race` 加退休 writer 隔离测试通过，exit 0（3.680s）。
- `30717`：补孤立 pending 意图拒绝退出及既有 preview 退出边界后再次 `-race` 通过，exit 0（3.742s）。

## 未宣称完成的边界

上述是隔离源码/真实本地文件及 IPC 测试证据，未操作正在运行的 App、服务、用户配置或真实模型。保存阅读位置及编辑路由，不声称已验证 Native 文本插入点/编辑区内部滚动恢复。冷停服启动不自动恢复草稿，须先明确启动服务并取得当前 identity。
