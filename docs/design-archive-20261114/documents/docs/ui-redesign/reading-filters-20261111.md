# F 筛选集成回归与来源身份修正

2026-11-11，沿用当前F候选与C三栏阅读，主Agent实现真实来源／范围类别／当前版本时间筛选后，本批补SQLite与UI集成回归，并修正来源名称与内部身份分离。不操作当前page3旧App、不发送其不支持的新filter参数、不跑真实模型或Native，也不完整打包。

## 生产修正

- `MemoryItem.RawSource`保存Version.Source原始键，显示Source继续走已认可“通用知识”命名。普通列表SQL仍用原始键；语义搜索snapshot的目录与匹配也用原始键，不把旧、新两种来源因同显示名混成一种。
- 下拉显示使用sourceDisplayName；旧、新来源同显示名并存时，旧项明确“旧记录”。自定义来源与旧标记同名时再区分选项，保留每个真实原始键。
- ConfigureLibrary清除旧来源目录、所属route和截断标记；切页面／项目尚未取得新目录时也不显示旧目录截断提示。语义搜索最多30条snapshot不沿用普通目录的1000种截断提示。
- 收紧旧未绑定游标兼容：只有Datasets／Kind／Source／Date／Text／ScopeKind／Timezone／Sort均空且UpdatedSinceMS为0的旧created-order查询可接受；任何带范围／类型／时间等过滤的旧游标明确拒绝，不猜scope。新fingerprint规则保持。

## 真实SQLite证据

`internal/store/reading_filters_integration_test.go`通过真实临时SQLite与Store.Apply保存／编辑／删除合成知识，覆盖：

1. ScopeKind、Source、UpdatedSinceMS组合过滤；时间依据当前有效版本，旧创建时间但新编辑的知识能匹配，旧版本不冒充当前保存。
2. 知识／原始记录计数先于过滤，类别不改变内容类型，已删除记录不进入当前来源目录；非法类别与负时间拒绝。
3. 前50条全部来自visible-source，较旧10条来自archive-only-source，目录仍包含后者；来源／时间过滤零结果也不把目录缩成当前页面采样，其他项目来源不混入。
4. 过滤分页完整遍历、无重复；修改scope、source、ScopeKind、时间、kind、text、sort、activity date或timezone后拒绝旧cursor。
5. 旧／新命名来源按真实原始键分别筛出对应记录；旧未绑定cursor的scope／kind／timezone拒绝，完全旧无过滤查询保留兼容。

## UI证据

`internal/desktop/reading_filters_test.go`覆盖来源别名身份、当前UpdatedAt筛选、仅筛已提交搜索snapshot、项目／搜索各自范围与来源时间条件返回、过滤零结果不误显“整理项目”、跨库来源目录退休，以及800×720／800×620筛选面板与下方列表可见和可选择。

还补真实下拉交互：同名项目带真实ID区分，旧命名来源选项保存原始键，选类别／今天后不丢此前scope与source。该交互测试的最终结果另补。

## 执行与视觉边界

- 真实SQLite首批84139通过exit0。
- desktop95397初次仅测试错误地要求720高的面板一定溢出而失败；实际控件在280px内全部可见、列表也可见，故将断言改为“小于720高需独立滚动，720高允许全部控件刚好容纳”，没有放宽分页、类别、来源或版本门禁。
- 组合CGO `-race` 20416（ReadingFilters／ScopeContentCounts／SearchTabs）通过exit0。
- `build/reading-filters-20261111/headless-final/`已有800×720展开与800×620滚到面板底部截图，已实际打开确认下方列表仍可阅读；图片为合成UI布局证据，不代表Native验收。
- 单独下拉交互17102在执行测试前被并行Provider图标重复“.svg.svg”导致的init panic阻断；由对应owner修正调用后重跑，未在本批回改HTTP文件。

本批不新增“工程约定”等原型示例分类。类别仅为真实scope.kind（project／preference／experience）；当前版本保存时间与首页daily活动日期保持独立。F完整Native普通／窄窗、真实大库与125场景验收仍交集中包，不以本切片关闭总Goal。

最终聚焦重跑25562通过exit0：真实dropdown选择、两种800宽面板／下方列表、跨route截断提示均通过；最终四张布局图已重新生成并再次实际打开800×720展开图。生产F文件已收口，后续manual drafts只向主Agent提供最小方案，不在本批追加源码。

最后仅重出合成布局证据38565通过exit0：fixture明确提供listTotal=1，并断言可见筛选总数“1 条知识”与列表一致，避免早期合成图未填该字段造成标题0／列表1的混淆；最终四张800布局图据此更新。本批没有因此重复全量测试或完整包。
