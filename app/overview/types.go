package overview

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

type StateOf[T any] struct {
	ExpandedEvent, ExpandedReport, CopiedEvent    string
	ExpandedDetail                                *FailureDetail
	ExpandedTechOpen                              bool
	RawLoaded                                     bool
	Snapshot                                      SnapshotOf[T]
	Loaded, Loading                               bool
	Error                                         string
	Events                                        []Event
	ActivityLoaded, ActivityLoading, More         bool
	ActivityError                                 string
	RecallError                                   string
	SearchHistory                                 bool
	History                                       []Event
	HistoryLoaded                                 bool
	HistoryError                                  string
	ActivityLimit                                 int
	Day                                           string
	ShowAllForDay                                 bool
	Offset                                        int
	Scroll                                        ui.ScrollState
	SidebarScroll                                 ui.ScrollState
	ActivityScroll                                ui.ScrollState
	KnowledgeScroll                               ui.ScrollState
	AllProjects                                   bool
	ActivityExpanded                              bool
	ScopeKey                                      string
	Generation, ActivityGeneration                uint64
	BootstrapCancel, ActivityCancel, RecallCancel context.CancelFunc
	ColdStartMotion                               bool
	PageRun, KnowledgeRun, ActivityRun            uint64
	SnapshotVersion                               uint64
	cachedBucketsVersion                          uint64
	cachedBucketsCount                            int
	cachedBucketsFirstKey                         string
	cachedBucketsLastKey                          string
	cachedBucketsDay                              string
	BucketCache                                   map[string]int
	cachedHeatmapVersion                          uint64
	cachedHeatmapCount                            int
	cachedHeatmapFirstKey                         string
	cachedHeatmapLastKey                          string
	cachedHeatmapDay                              string
	cachedHeatmapLoaded                           bool
	cachedHeatmapWeeks                            int
	HeatmapCache                                  []HeatmapCell
	HeatmapGrowStart                              time.Time
	heatmapTapDay                                 string
	heatmapTapStart                               time.Time
	heatmapTapPending                             bool
	heatmapRingPrevDay                            string
	heatmapRingStart                              time.Time
	heatmapRingPending                            bool
	LastNavIndY                                   float32
	activityFetchedAt                             time.Time
	cachedGroupedGen                              uint64
	cachedGroupedHistory                          bool
	cachedGroupedCount                            int
	GroupedEvents                                 []Event
	V2Mode                                        bool
	V2Generation                                  int64
	V2TotalKnowledge                              int
	V2DayBuckets                                  map[string]int
	V2RecallStartedAt                             string
	DayItems                                      []T
	DayList                                       ui.ListState
	DayTotal                                      int
	DayNextCursor                                 string
	DayHasMore                                    bool
	DayLoading                                    bool
	DayError                                      string
	DayRequest                                    uint64
	DayCancel                                     context.CancelFunc
}

type HeatmapCell struct {
	Week, Weekday int
	Date          string
	Level         int
}
type FirstUseKey struct {
	clientState, config, day string
}

type FirstUseState struct {
	CachedKey          FirstUseKey
	LoadingKey         FirstUseKey
	cachedFirstUseDays int
	Err                error
	Request            uint64
}
type SnapshotOf[T any] struct {
	Knowledge    []T
	Raw          int
	MissingDates int
	RawError     string
}

type FirstUseReceipt struct {
	Version   int    `json:"schema_version"`
	Completed string `json:"initialization_completed_at"`
	Date      string `json:"first_used_date"`
	Zone      string `json:"first_used_timezone"`
}
type ActivityRow = api.ActivityRow
type Event = api.ActivityEvent
type RecallHit = api.ActivityRecallHit
type RecallEvent = api.ActivityRecallEvent
type FailureDetailItem struct {
	StageLabel  string
	StageRaw    string
	ErrorClass  string
	ReasonLabel string
	LocalTime   string
	Duration    string
}

type FailureTechRecord struct {
	StageLabel      string
	StageRaw        string
	ErrorClass      string
	ReasonLabel     string
	IsParentWrapper bool
	ID              string
	Run             string
	Parent          string
	Dataset         string
	Origin          string
	CreatedUTC      string
	StartedUTC      string
	EndedUTC        string
	LogCandidate    string
}

type FailureDetail struct {
	EventKey        string
	Datasets        []string
	DialogTitle     string
	OperationTitle  string
	Project         string
	ReasonLabel     string
	ErrorClass      string
	Summary         string
	Hint            string
	StageLabel      string
	StageRaw        string
	LocalTime       string
	Duration        string
	EventUTC        string
	FailureCount    int
	Items           []FailureDetailItem
	ServiceEndpoint string
	ServiceCommit   string
	LogDir          string
	HasLogCandidate bool
	TechRecords     []FailureTechRecord
	BoundaryNote    string
}

type Item struct {
	ID, ProjectID, Dataset, Kind, Title, Date, Preview, SourceDocument string
	ReadingState                                                       string
	TitlePending, Unread                                               bool
	RecallCount                                                        int
	Category                                                           string
}

func (i Item) Key() string { return i.Dataset + "/" + i.Kind + "/" + i.ID }

type Snapshot = SnapshotOf[Item]
type State = StateOf[Item]

type Hydration[T any] struct {
	Snapshot            SnapshotOf[T]
	Generation          int64
	TotalKnowledge      int
	Buckets             map[string]int
	RecallStartedAt     string
	Events, History     []Event
	More, HistoryLoaded bool
	Projects            []api.ProjectSummary
}

type DayPage[T any] struct {
	Items      []T
	Total      int
	NextCursor string
	HasMore    bool
}

type RecallGroup struct {
	Dataset, Name string
	Count         int
}
