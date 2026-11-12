package desktop

import "github.com/egoist/mygo/ui"

// Unknown measurements remain unknown, never copied from the prototype.
func (a *App) environmentDistribution(c *ui.Context, cards []environmentCard) {
	download, occupied := int64(0), int64(0)
	knownDownload, knownOccupied := true, true
	for _, card := range cards {
		if len(card.Items) == 0 {
			knownDownload, knownOccupied = false, false
		}
		for _, item := range card.Items {
			download += item.Bytes
			if item.State == "not_verified" || (!preparedEnvironmentItem(item.State) && item.ExpectedBytes <= 0) {
				knownDownload = false
			}
			if item.OccupiedBytes == nil {
				knownOccupied = false
			} else {
				occupied += *item.OccupiedBytes
			}
		}
	}
	downloadText, occupiedText := "尚未取得下载量", "尚未取得占用测量"
	if knownDownload {
		downloadText = environmentSize(download)
	}
	if knownOccupied {
		occupiedText = environmentSize(occupied)
	}
	settingsCard(c, "environment-distribution", "存储与分发", func() {
		settingsFact(c, "主客户端", "本机安装包大小", "尚未取得包测量")
		ui.Divider(c)
		settingsFact(c, "组件额外下载", "仅准备缺失或不兼容项", downloadText)
		ui.Divider(c)
		settingsFact(c, "组件磁盘占用", "含运行依赖与模型", occupiedText)
	})
}
