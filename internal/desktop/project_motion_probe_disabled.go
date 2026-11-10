//go:build !e2e

package desktop

import "time"

func projectDrawStart() time.Time { return time.Time{} }

func (a *App) recordProjectDraw(string, time.Time, time.Time, bool, bool, float32, float32, [4]float32) {
}
