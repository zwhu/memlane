# 三条历史知识正文文件缺失排查

后续状态：用户授权后，三份正文已于当天15:15恢复并经实际 raw API 核验。19:15完成的全库引用检查又恢复另外14份缺失正文和92个向量分片，将同一日常库迁入持久目录，详见[分片恢复记录](vector-fragment-missing-20261118.md)。以下保留最初只读诊断事实，不代表当前仍未执行恢复；具体删除者仍未知，未来删除审计尚未实现。

2026-11-18。用户要求调查召回9次但正文不可读，并提出是否整理时重复删除。本轮只读诊断，没有修改官方数据库、复制回原文、重跑整理或删除记录。

## 已证事实

- 项目dataset为0785e3d2-881a-58c4-883e-cc3d2dcf76ef；Data ID为83fcf5dd-4521-53db-8559-4837c9a7912d、ada020e3-7444-524f-a462-d034940c1e33、f55f8c96-7d1a-5a64-9b7e-25edc08ad9aa。
- 3条Data记录仍在，raw_data_location/original_data_location均指向原日常根data/2417bc94-b3a0-439e-8dc8-c87b8d8271da下的text_*.txt；现路径不存在，官方X-Api-Key读取均404 Raw file not found on disk。
- 每条9次已有automatic回执；最后一次UTC2026-11-15 15:36:05.434312，即北京时间11月15日23:36。统计不是当前可读证明，未发现之后新增回执。
- store-merge-20261114/backup/daily-official中3份原文件均存在，大小349/308/375字节，MD5与Data.raw_content_hash一致，SHA256已留私有诊断回执。
- 合库verification.json及verify_union.py记录当时37份原文均存在且与备份SHA256一致。现有日志最早缺失记录为UTC2026-11-16 01:50:43，即北京时间11月16日09:50；缺失发生的精确时间仍未知，不能拿第一次读取失败当作删除时间。
- t5-invalid-code-cleanup/t5-official-scoped-drop中的这3个ID属于primary_document_ids_before/after保留清单，并非删除对象；当时目标是独立code dataset且时间早于完整合库核验，不作为本次误删证据。

## 假设与排除边界

1. 整理替换/重复知识删除误清共享文件：官方lesson_store.replace_superseded_lessons会调用datasets.delete_data，当前delete_data_entity先提交删除Data再按路径引用计数清文件；3条Data仍在不符合这些条目正常去重删除的结果。但其他重复行清共享文件、历史版本缺陷或部分失败仍不能仅凭当前源码排除。
2. 合库漏拷文件：与当时逐份原文核验通过的回执不符，现有证据不支持。
3. 后续清理/路径引用变化：尚未找到执行者/删除时间/精确删除回执。11月17日旧组件清理晚于已知首次缺失，不构成已证触发。

当前结论：不是已证明的正常整理去重；是Data目录记录存续而正文文件缺失的数据一致性异常。3份哈希匹配的原文可用于精确恢复，尚未执行恢复。显示层将Items.RecallStats清空但Catalog仍按9排序是独立已证缺陷，尚未修复。

## 证据与方法

私有build/diagnostics/raw-file-audit-20261118.json存目标元数据、备份大小/哈希与路径核对；build/diagnostics/recall-unavailable-20261118.json存原文GET结果。读取活动SQLite原路径mode=ro失败后，在临时诊断目录复制稳定DB及存在的WAL，复制前后校验源SHA256一致，再查询临时副本并立即移除；没有打开原库为可写或改写原库。未将单测/快照当成原环境修复验收，未累计修复验收失败轮次。
