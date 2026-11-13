# 新宿主明确拒绝与未知结果

MA-ACL-REPLY01，真实权限检查失败1轮：只读TraeX主体已正确禁止显式remember／forget，却被JSON helper的通用异常出口写成`unavailable / memory_operation_unconfirmed`。没有发现越权写入／删除；问题在已知拒绝被误呈现为未知结果。

修复：显式写／删除先用实时`current(dataset)`确认官方有效ACL，再返回`denied`、403和固定错误码。读取策略失败仍保留unconfirmed，不误报权威拒绝；官方返回401／403单独归类，异常文本、响应正文和密钥不返回宿主。其他传输、Schema或超时未知继续沿原防重放路径，不因为增加错误分类重投请求。自动采集的`skipped_policy`及无正文水位保持。

在原独立库、原pi／TraeX主体与原会话上复验；先确认官方处理全部完成，再通过正常dev停止入口确认进程组退出，重启同一库加载当前源码，准备／激活不可变新helper。没有重建主体、清空库或换会话重置计数。

- 初始两主体维护权限，均能召回原项目1886字符。
- TraeX改只读：召回保留；自动采集跳过；显式写／删除返回明确403；绕过helper直达官方写／删除接口也返回403。
- 改为不可访问：原会话召回变空、官方raw读取403，pi仍保持维护权限。
- 恢复动态默认策略：原会话召回恢复；重发同一被禁采事件返回原skipped_policy duplicate，不补交。官方会话接口404，未创建这段被拒绝材料的官方记录。
- 第二个规范项目被两个主体映射到另一个共同dataset，召回不混入第一个项目知识；临时改变旧会话cwd不改变其已绑定dataset。
- 在第二个项目，两个主体分别通过真实helper显式remember合成文档，核对官方Data和raw，等待官方处理完成，再指定单条DataID forget并确认Data消失。原项目及官方dataset保留，没有整库删除。
- 官方ACL联合核对包含直接、tenant及role：两主体独立子身份，项目read/write/delete，均无share；通用知识维持read。

修复后通过，失败计数仍为1。证据见[real-agent-permissions.json](evidence/multi-agent-development-20261114/real-agent-permissions.json)。定向7项、全量后端104项通过。此权限矩阵为已部署helper＋真实HTTP／官方身份，不冒充新增的正常CLI模型会话；此前普通宿主知识采用证据另见real-coding-hosts。

方法错误保留：raw端点是UTF-8正文，第一次夹具误按JSON解析，发生在本轮helper断言前；Data.name与extension分开，首次相关断言误含`.txt`；后台remember接受不等于Data立刻可见，首次TraeX Data观察过早。对原已接受写入仅进行只读核对与有界轮询，没有重发remember；私有attempt账本保存原结果，再继续单条删除。三者不计成权限实现失败。

仍待覆盖：403发生在buffer实际提交阶段的竞态／显式补交、通用知识有内容时的项目优先、其他宿主权限和Native连续窗口。本轮没有修改原日常服务、登录、库、其他插件或共享组件。
