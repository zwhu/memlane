package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"time"
)

type piCaptureSettingsUI struct {
	Connection            string
	Generation            int64
	Rows                  []records.PiCaptureSetting
	Loaded, Loading, Busy bool
	Sequence              uint64
	Message               string
	Review                *records.PiCaptureConsentReview
	Disable               *records.PiCaptureConsentDisable
}

func (a *App) piCaptureState(connection records.Connection) *piCaptureSettingsUI {
	s := a.agents.PiCapture
	if s == nil || s.Connection != connection.ID || s.Generation != connection.Generation {
		s = &piCaptureSettingsUI{Connection: connection.ID, Generation: connection.Generation}
		a.agents.PiCapture = s
	}
	return s
}

func (a *App) piCaptureVisible(s *piCaptureSettingsUI) bool {
	_, c := a.selectedAgent()
	return a.page == "设置" && a.settingsSection == "agents" && c != nil && c.ID == s.Connection && c.Generation == s.Generation
}

func (a *App) refreshPiCaptureSettings(connection records.Connection) {
	if connection.Agent != "pi" || a.libraryContext == nil || a.agents.Dispatch == nil {
		return
	}
	s := a.piCaptureState(connection)
	if s.Loading || s.Busy || !a.piCaptureVisible(s) {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence, epoch, client, life, dispatch := s.Sequence, a.libraryEpoch, a.library, a.libraryContext, a.agents.Dispatch
	go func() {
		ctx, cancel := context.WithTimeout(life, 12*time.Second)
		defer cancel()
		rows, err := client.PiCaptureSettings(ctx, records.PiCaptureSettingsQuery{Connection: connection.ID, Generation: connection.Generation})
		dispatch(func() {
			if life.Err() != nil || a.libraryEpoch != epoch || a.library.Root != client.Root || a.agents.PiCapture != s || s.Sequence != sequence {
				return
			}
			s.Loading = false
			if !a.piCaptureVisible(s) {
				s.Loaded = false
				return
			}
			if err != nil {
				s.Loaded = false
				if !sourceReaderTransientFailure(sourceReaderErrorCode(err)) {
					s.Rows = nil
					s.Review = nil
					s.Disable = nil
				}
				s.Message = "记录权限暂时无法核对，请重新读取。"
				return
			}
			s.Rows = rows
			s.Loaded = true
			if s.Review == nil && s.Disable == nil {
				s.Message = ""
			}
		})
	}()
}

func (a *App) changePiCapture(s *piCaptureSettingsUI) {
	if s.Busy || !s.Loaded || !a.piCaptureVisible(s) || a.libraryContext == nil || a.agents.Dispatch == nil || (s.Review == nil && s.Disable == nil) {
		return
	}
	review, disable := s.Review, s.Disable
	s.Sequence++
	s.Loading = false
	s.Busy = true
	epoch, client, life, dispatch := a.libraryEpoch, a.library, a.libraryContext, a.agents.Dispatch
	s.Message = "正在保存记录权限…"
	go func() {
		ctx, cancel := context.WithTimeout(life, 15*time.Second)
		defer cancel()
		var err error
		if review != nil {
			var receipt records.PiCaptureConsent
			receipt, err = client.ReviewPiCaptureConsent(ctx, *review)
			if err == nil && (receipt.Operation != review.Operation || receipt.Binding != review.Binding || receipt.PreviousKey != review.PreviousKey || !receipt.Enabled) {
				err = errors.New("capture consent receipt changed")
			}
		} else {
			err = client.DisablePiCaptureConsent(ctx, *disable)
		}
		dispatch(func() {
			if life.Err() != nil || a.libraryEpoch != epoch || a.library.Root != client.Root || a.agents.PiCapture != s {
				return
			}
			s.Busy = false
			if err != nil {
				s.Message = "尚未确认保存结果。请核对并重试同一操作。"
				return
			}
			s.Review = nil
			s.Disable = nil
			s.Message = "权限已保存，正在重新核对。"
			_, connection := a.selectedAgent()
			if connection != nil && a.piCaptureVisible(s) {
				a.refreshPiCaptureSettings(*connection)
			} else {
				s.Loaded = false
			}
		})
	}()
}

func (a *App) piCaptureSettings(c *ui.Context, connection records.Connection) {
	s := a.piCaptureState(connection)
	installation, installed := a.agents.Installs[connection.ID]
	supported := installed && installation.Connection.Generation == connection.Generation && installation.PiCaptureSupported && a.agents.InstallErrors[connection.ID] == ""
	ui.Column(c).Key("pi-daily-capture").Gap(10).Children(func() {
		ui.Text(c, "正常对话记录").Bold()
		ui.Text(c, "按项目确认后，记录 pi 新增的问答和工具结果。不导入历史，也不自动整理为知识。停止记录保留已保存内容。").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.7)
		if !supported {
			ui.Text(c, "当前采集扩展尚未核对，请先更新接入并验证兼容版本。").FontSize(design.Small).TextColor(design.Muted)
		}
		quietButton(c, "读取记录权限").Key("pi-capture-refresh").Disabled(s.Busy || s.Loading || a.libraryContext == nil || !connection.Enabled).OnClick(func() { a.refreshPiCaptureSettings(connection) })
		if !s.Loaded {
			ui.Text(c, "记录权限尚未核对。").FontSize(design.Small).TextColor(design.Muted)
		}
		if s.Loaded && len(s.Rows) == 0 {
			ui.Text(c, "尚无已授权且已配置保存位置的项目。请在项目设置中完成接入后重新读取。").FontSize(design.Small).TextColor(design.Muted)
		}
		for _, row := range s.Rows {
			row := row
			key := row.Binding.Project + "-" + row.Binding.Instance + "-" + row.Binding.Dataset
			ui.Column(c).Key("pi-capture-project-"+key).Gap(8).Padding(12).Border(1, design.Border).Radius(6).Children(func() {
				label := "未开启"
				if !s.Loaded {
					label = "记录权限待核对"
				} else if !row.Ready {
					label = "连接范围已变化"
				} else if row.Consent != nil {
					if row.Consent.Enabled {
						label = "已允许新对话"
					} else {
						label = "已停止记录"
					}
				}
				settingsFact(c, a.scopeLabel(row.Binding.Project), "仅限此项目授权后的新记录", label)
				if row.Saved > 0 {
					ui.Text(c, fmt.Sprintf("已保存 %d 条问答或工具记录", row.Saved)).FontSize(design.Small).TextColor(design.Muted)
				}
				if s.Review != nil && s.Review.Binding == row.Binding {
					ui.Text(c, "确认允许记录此项目的新问答与工具结果。请重新打开 pi 以启用；是否已接收内容以保存回执为准。").FontSize(design.Small).LineHeight(1.7)
					ui.Row(c).Wrap().Gap(8).Children(func() {
						quietButton(c, "取消").Disabled(s.Busy).OnClick(func() { s.Review = nil; s.Message = ""; a.refreshPiCaptureSettings(connection) })
						actionButton(c, "确认开启记录", nil).Key("pi-capture-confirm").Disabled(s.Busy || !s.Loaded || !supported || !connection.Enabled).OnClick(func() { a.changePiCapture(s) })
					})
				} else if s.Review == nil && s.Disable == nil {
					if row.Consent != nil && row.Consent.Enabled {
						quietButton(c, "停止记录").Key("pi-capture-stop-" + key).Disabled(s.Busy || s.Loading || !s.Loaded || !row.Ready).OnClick(func() {
							s.Disable = &records.PiCaptureConsentDisable{Connection: connection.ID, Project: row.Binding.Project, ExpectedKey: row.Consent.Key}
							a.changePiCapture(s)
						})
					} else {
						quietButton(c, "开启记录").Key("pi-capture-enable-" + key).Disabled(s.Busy || s.Loading || !s.Loaded || !row.Ready || !supported || !connection.Enabled).OnClick(func() {
							s.Review = &records.PiCaptureConsentReview{Operation: uuid.NewString(), PreviousKey: row.PreviousKey, Binding: row.Binding}
							s.Message = ""
						})
					}
				}
			})
		}
		if s.Disable != nil && !s.Busy {
			quietButton(c, "核对并重试停止记录").OnClick(func() { a.changePiCapture(s) })
		}
		if s.Message != "" {
			ui.Text(c, s.Message).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.7)
		}
	})
}
