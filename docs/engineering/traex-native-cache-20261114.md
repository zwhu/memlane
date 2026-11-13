# TraeX 原生缓存与代际保护

当前修复已通过真实0.214.1安装器的隔离profile检查；没有在真实日常profile重装，也不以安装检查代替正常CLI知识闭环。

| 编号 | 触发与已证原因 | 失败计数与结果 |
| --- | --- | --- |
| MA-TRAE-CACHE01 | 旧产物名为cognee，原生install的`--name memlane-cognee`重写缓存manifest，完整摘要不一致；其余文件和私有绑定逐项一致 | 旧真链缓存只读核对失败1次；准备阶段即生成目标名称、取消name覆盖后完整缓存检查通过 |
| MA-TRAE-VERSION01 | 为保护缓存引入合法SemVer build版本后，本地路径校验未允许`+`，安装成功却被管理侧拒绝 | 定向检查失败1次；允许安全的`+`字符后该断言通过，仍拒绝斜杠与dot父目录 |
| MA-TRAE-CACHE02 | 原生install清理同marketplace同插件的旧cache版本，单加版本号无法保护运行中宿主 | 两代安装保护检查失败1次；独立无hook合成插件诊断确认原生清理行为；每代独立marketplace后两代缓存完整性通过 |

不合并上述不同触发条件凑停止次数；每个失败证据仍保留于本机`.local/agent-native-cache-test*.log`。MA-TRAE-CACHE02不因换namespace清零，修复后的通过是同问题复验。

准备阶段把名称与含代际的版本写入受管manifest后再计算摘要。原生install通过显式`--marketplace-name`指向本代命名空间，缓存路径与源产物均不可变；激活新代后只disable登记的原自有入口，不卸载或删除旧缓存、不改其他插件。任一命令失败仍保留activation_unknown，不自动重跑安装。

管理侧核对本代direct-install原生回执的type、marketplace、完整source路径／kind，当前启用键、没有另一个已启用Cognee入口，cache内全部固定文件以及私有安装绑定。未知操作review只读取同一批原产物和原生回执，不执行install／disable，也不把部分配置当完成。GET连接状态发现文件被改动或已禁用时显示需核对。

定向7项检查中，真实安装器用例连续完成首次安装、unknown→只读review、cache篡改拒绝、显式第二代激活、第一代cache仍可完整验签、禁用拒绝；原自有身份／凭据不重建。全量后端104项通过。pi真实loader另验4工具、1问答、1错误trace及撤权撤下上下文；普通编码真链与边界见[real-coding-hosts.json](evidence/multi-agent-development-20261114/real-coding-hosts.json)。

一次loader检查误传旧npm scope路径，导入前失败；根据当前pi可执行symlink改为实际`@earendil-works/pi-coding-agent`后通过。这是命令前置错误，不计宿主采集或模型失败。未改用户默认环境或安装新依赖。
