package simpledesktop

import (
	"time"

	appoverview "github.com/zwhu/memlane/app/overview"
)

type failureDetailModel = appoverview.FailureDetail

var describeFailureClass = appoverview.DescribeFailureClass
var eventFailureReason = appoverview.EventFailureReason
var formatDurationZH = appoverview.FormatDurationZH
var failureLogCandidate = appoverview.FailureLogCandidate
var formatLocalWindow = appoverview.FormatLocalWindow

func overviewDiagnosticSource(c Config) appoverview.DiagnosticSource {
	return appoverview.DiagnosticSource{ServiceURL: c.ServiceURL, ServiceCommit: c.ServiceCommit, OfficialData: c.OfficialData}
}

func buildFailureDetail(event overviewEvent, c Config, loc *time.Location) failureDetailModel {
	return appoverview.BuildFailureDetail(event, overviewDiagnosticSource(c), loc)
}

func failureReport(event overviewEvent, c Config) string {
	return appoverview.FailureReport(event, overviewDiagnosticSource(c))
}
