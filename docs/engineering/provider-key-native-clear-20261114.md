# Provider 密钥输入的 Native 残留

问题编号MA-UI-KEY01，累计验收失败1轮，第二轮实际Native复验通过；失败记录不清零。

原目标：Provider保存后清空新输入的密钥，不回填已保存密钥；界面及辅助功能不能继续保留已提交值。场景仅使用明确合成的无效测试key和隔离管理服务，没有真实凭据、真实模型或日常库。

首轮实际1200×820窗口：保存成功，密码输入画面回到placeholder，Go草稿字段为空，验证按钮已解锁；但AX secure text field的Value仍是刚输入的合成字符串。切换焦点、重新读取完整AX树后仍可见。单纯渲染截图和`Texts()`检查没有发现它。

已证事实：复用同一控件Key时，Native辅助功能值没有随业务字段清空；更换控件代次后旧值从AX树移除。尚未追踪SDK／系统辅助功能内部的具体缓存位置，不将这一现象外推为全部TextInput的已证根因。

修复：`integrationState.KeyEpoch`在提交和切换配置时递增；Provider表单控件按配置ID及代次设置稳定Key。业务字段立即清空，已提交输入控件退出；新控件以空值初始化。未提交的普通编辑保持同一代次，后台刷新不会丢草稿。复用官方Password、Form和TextInput，没有新增自绘密码框或改全局SDK。

第二轮实际Native：重新输入另一组合成key并保存，完整AX树的secure text field没有Value，画面placeholder为空；已保存标记、验证按钮、当前模型仍为原配置。重开窗口后仅显示已配置密钥标记，模型及endpoint可读，key不回填。证据为本次CUA操作回执及[Native检查点](evidence/multi-agent-development-20261114/native-checkpoint.json)，合成图在同目录`ui-fixtures/`；截图不能替代这项AX核对。

后续涉及密码清空的改动，除视觉和业务状态外，应核对真实Native辅助功能值；不把Password的视觉遮罩当作控件生命周期已清理的证据。输入过程中的临时值与后端密钥回填是不同边界。本次没有宣称内存安全擦除。
