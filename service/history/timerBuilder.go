package history

import (
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/uber-common/bark"
	w "github.com/uber/cadence/.gen/go/shared"
	"github.com/uber/cadence/common"
	"github.com/uber/cadence/common/persistence"
)

// Timer constansts
const (
	TimerQueueSeqNumBits                  = 26 // For timer-queues, use 38 bits of (expiry) timestamp, 26 bits of seqnum
	TimerQueueSeqNumBitmask               = (int64(1) << TimerQueueSeqNumBits) - 1
	TimerQueueTimeStampBitmask            = math.MaxInt64 &^ TimerQueueSeqNumBitmask
	SeqNumMax                             = math.MaxInt64 & TimerQueueSeqNumBitmask // The max allowed seqnum (subject to mode-specific bitmask)
	MinTimerKey                SequenceID = -1
	MaxTimerKey                SequenceID = math.MaxInt64

	DefaultScheduleToStartActivityTimeoutInSecs = 10
	DefaultScheduleToCloseActivityTimeoutInSecs = 10
	DefaultStartToCloseActivityTimeoutInSecs    = 10

	emptyTimerID = -1
)

type (
	timerDetails struct {
		SequenceID  SequenceID
		TimerTask   persistence.Task
		TaskCreated bool
	}

	timers []*timerDetails

	timerBuilder struct {
		timers            timers
		pendingUserTimers map[SequenceID]*persistence.TimerInfo
		logger            bark.Logger
		seqNumGen         SequenceNumberGenerator // The real sequence number generator
		localSeqNumGen    SequenceNumberGenerator // This one used to order in-memory list.
	}

	// SequenceID - Visibility timer stamp + Sequence Number.
	SequenceID int64

	// SequenceNumberGenerator - Generates next sequence number.
	SequenceNumberGenerator interface {
		NextSeq() int64
	}

	localSeqNumGenerator struct {
		counter int64
	}

	shardSeqNumGenerator struct {
		context ShardContext
	}
)

// ConstructTimerKey forms a unique sequence number given a expiry and sequence number.
func ConstructTimerKey(expiryTime int64, seqNum int64) SequenceID {
	return SequenceID((expiryTime & TimerQueueTimeStampBitmask) | (seqNum & TimerQueueSeqNumBitmask))
}

// DeconstructTimerKey decomoposes a unique sequence number to an expiry and sequence number.
func DeconstructTimerKey(key SequenceID) (expiryTime int64, seqNum int64) {
	return int64(int64(key) & TimerQueueTimeStampBitmask), int64(int64(key) & TimerQueueSeqNumBitmask)
}

func (s SequenceID) String() string {
	expiry, seqNum := DeconstructTimerKey(s)
	return fmt.Sprintf("SequenceID=%v(%x %x) %s", int64(s), expiry, seqNum, time.Unix(0, int64(expiry)))
}

// Len implements sort.Interace
func (t timers) Len() int {
	return len(t)
}

// Swap implements sort.Interface.
func (t timers) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

// Less implements sort.Interface
func (t timers) Less(i, j int) bool {
	return t[i].SequenceID < t[j].SequenceID
}

func (td *timerDetails) String() string {
	return fmt.Sprintf("timerDetails: [%s expiry=%s]", td.SequenceID, time.Unix(0, int64(td.SequenceID)))
}

func (s *shardSeqNumGenerator) NextSeq() int64 {
	return s.context.GetTimerSequenceNumber()
}

func (l *localSeqNumGenerator) NextSeq() int64 {
	return atomic.AddInt64(&l.counter, 1)
}

// newTimerBuilder creates a timer builder.
func newTimerBuilder(seqNumGen SequenceNumberGenerator, logger bark.Logger) *timerBuilder {
	return &timerBuilder{
		timers:            timers{},
		pendingUserTimers: make(map[SequenceID]*persistence.TimerInfo),
		logger:            logger.WithField(tagWorkflowComponent, "timer"),
		seqNumGen:         seqNumGen,
		localSeqNumGen:    &localSeqNumGenerator{counter: 1}}
}

// AllTimers - Get all timers.
func (tb *timerBuilder) AllTimers() timers {
	return tb.timers
}

// UserTimer - Get a specific timer info.
func (tb *timerBuilder) UserTimer(taskID SequenceID) (bool, *persistence.TimerInfo) {
	ti, ok := tb.pendingUserTimers[taskID]
	return ok, ti
}

// AddDecisionTimoutTask - Add a decision timeout task.
func (tb *timerBuilder) AddDecisionTimoutTask(scheduleID int64,
	builder *historyBuilder) *persistence.DecisionTimeoutTask {
	startWorkflowExecutionEvent := builder.GetEvent(firstEventID)
	startAttributes := startWorkflowExecutionEvent.GetWorkflowExecutionStartedEventAttributes()
	timeOutTask := tb.createDecisionTimeoutTask(startAttributes.GetTaskStartToCloseTimeoutSeconds(), scheduleID)
	return timeOutTask
}

func (tb *timerBuilder) AddScheduleToStartActivityTimeout(scheduleID int64, scheduleEvent *w.HistoryEvent,
	msBuilder *mutableStateBuilder) *persistence.ActivityTimeoutTask {
	scheduleToStartTimeout := scheduleEvent.GetActivityTaskScheduledEventAttributes().GetScheduleToStartTimeoutSeconds()
	if scheduleToStartTimeout <= 0 {
		scheduleToStartTimeout = DefaultScheduleToStartActivityTimeoutInSecs
	}
	scheduleToCloseTimeout := scheduleEvent.GetActivityTaskScheduledEventAttributes().GetScheduleToCloseTimeoutSeconds()
	if scheduleToCloseTimeout <= 0 {
		scheduleToCloseTimeout = DefaultScheduleToCloseActivityTimeoutInSecs
	}
	startToCloseTimeout := scheduleEvent.GetActivityTaskScheduledEventAttributes().GetStartToCloseTimeoutSeconds()
	if startToCloseTimeout <= 0 {
		startToCloseTimeout = DefaultStartToCloseActivityTimeoutInSecs
	}
	heartbeatTimeout := scheduleEvent.GetActivityTaskScheduledEventAttributes().GetHeartbeatTimeoutSeconds()

	t := tb.AddActivityTimeoutTask(scheduleID, w.TimeoutType_SCHEDULE_TO_START, scheduleToStartTimeout)

	msBuilder.UpdatePendingActivity(scheduleID, &persistence.ActivityInfo{
		ScheduleID:             scheduleID,
		StartedID:              emptyEventID,
		ActivityID:             scheduleEvent.GetActivityTaskScheduledEventAttributes().GetActivityId(),
		ScheduleToStartTimeout: scheduleToStartTimeout,
		ScheduleToCloseTimeout: scheduleToCloseTimeout,
		StartToCloseTimeout:    startToCloseTimeout,
		HeartbeatTimeout:       heartbeatTimeout,
		CancelRequested:        false,
		CancelRequestID:        emptyEventID,
	})

	return t
}

func (tb *timerBuilder) AddScheduleToCloseActivityTimeout(scheduleID int64,
	msBuilder *mutableStateBuilder) (*persistence.ActivityTimeoutTask, error) {
	ok, ai := msBuilder.isActivityRunning(scheduleID)
	if !ok {
		return nil, fmt.Errorf("ScheduleToClose: Unable to find activity Info in mutable state for event id: %d", scheduleID)
	}
	return tb.AddActivityTimeoutTask(scheduleID, w.TimeoutType_SCHEDULE_TO_CLOSE, ai.ScheduleToCloseTimeout), nil
}

func (tb *timerBuilder) AddStartToCloseActivityTimeout(scheduleID int64,
	msBuilder *mutableStateBuilder) (*persistence.ActivityTimeoutTask, error) {
	ok, ai := msBuilder.isActivityRunning(scheduleID)
	if !ok {
		return nil, fmt.Errorf("StartToClose: Unable to find activity Info in mutable state for event id: %d", scheduleID)
	}
	return tb.AddActivityTimeoutTask(scheduleID, w.TimeoutType_START_TO_CLOSE, ai.StartToCloseTimeout), nil
}

func (tb *timerBuilder) AddHeartBeatActivityTimeout(scheduleID int64,
	msBuilder *mutableStateBuilder) (*persistence.ActivityTimeoutTask, error) {
	ok, ai := msBuilder.isActivityRunning(scheduleID)
	if !ok {
		return nil, fmt.Errorf("HeartBeat: Unable to find activity Info in mutable state for event id: %d", scheduleID)
	}
	return tb.AddActivityTimeoutTask(scheduleID, w.TimeoutType_HEARTBEAT, ai.HeartbeatTimeout), nil
}

// AddActivityTimeoutTask - Adds an activity timeout task.
func (tb *timerBuilder) AddActivityTimeoutTask(scheduleID int64,
	timeoutType w.TimeoutType, fireTimeout int32) *persistence.ActivityTimeoutTask {
	if fireTimeout <= 0 {
		return nil
	}

	timeOutTask := tb.createActivityTimeoutTask(fireTimeout, timeoutType, scheduleID)
	tb.logger.Debugf("Adding Activity Timeout: %+v", timeOutTask)
	return timeOutTask
}

// AddUserTimer - Adds an user timeout request.
func (tb *timerBuilder) AddUserTimer(timerID string, fireTimeout int64, startedID int64,
	msBuilder *mutableStateBuilder) (persistence.Task, error) {
	if fireTimeout < 0 {
		return nil, fmt.Errorf("Invalid user timerout specified")
	}

	if isRunning, ti := msBuilder.isTimerRunning(timerID); isRunning {
		return nil, fmt.Errorf("The timer ID already exist in activity timers list: %s, old timer: %+v", timerID, *ti)
	}

	tb.logger.Debugf("Adding User Timer: %s", timerID)

	// TODO: Time skew need to be taken in to account.
	expiryTime := time.Now().Add(time.Duration(fireTimeout) * time.Second)
	msBuilder.UpdatePendingTimers(timerID, &persistence.TimerInfo{
		TimerID:    timerID,
		ExpiryTime: expiryTime,
		StartedID:  startedID,
		TaskID:     emptyTimerID,
	})
	tb.LoadUserTimers(msBuilder)

	timerTask := tb.firstTimer()
	if timerTask != nil {
		// Update the task ID tracking the corresponding timer task.
		ti := tb.pendingUserTimers[tb.timers[0].SequenceID]
		ti.TaskID = timerTask.GetTaskID()
		msBuilder.UpdatePendingTimers(ti.TimerID, ti)
	}

	return timerTask, nil
}

// LoadUserTimers - Load all user timers from mutable state.
func (tb *timerBuilder) LoadUserTimers(msBuilder *mutableStateBuilder) {
	tb.timers = timers{}
	tb.pendingUserTimers = make(map[SequenceID]*persistence.TimerInfo)
	for _, v := range msBuilder.pendingTimerInfoIDs {
		td, _ := tb.loadUserTimer(v.ExpiryTime.UnixNano(),
			&persistence.UserTimerTask{EventID: v.StartedID},
			v.TaskID != emptyTimerID)
		tb.pendingUserTimers[td.SequenceID] = v
	}
}

// IsTimerExpired - Whether a timer is expired w.r.t reference time.
func (tb *timerBuilder) IsTimerExpired(td *timerDetails, referenceTime int64) bool {
	expiry, _ := DeconstructTimerKey(td.SequenceID)
	return expiry <= referenceTime
}

// createDecisionTimeoutTask - Creates a decision timeout task.
func (tb *timerBuilder) createDecisionTimeoutTask(fireTimeOut int32, eventID int64) *persistence.DecisionTimeoutTask {
	expiryTime := common.AddSecondsToBaseTime(time.Now().UnixNano(), int64(fireTimeOut))
	seqID := ConstructTimerKey(expiryTime, tb.seqNumGen.NextSeq())
	return &persistence.DecisionTimeoutTask{
		TaskID:  int64(seqID),
		EventID: eventID,
	}
}

// createActivityTimeoutTask - Creates a activity timeout task.
func (tb *timerBuilder) createActivityTimeoutTask(fireTimeOut int32, timeoutType w.TimeoutType, eventID int64) *persistence.ActivityTimeoutTask {
	expiryTime := common.AddSecondsToBaseTime(time.Now().UnixNano(), int64(fireTimeOut))
	seqID := ConstructTimerKey(expiryTime, tb.seqNumGen.NextSeq())
	return &persistence.ActivityTimeoutTask{
		TaskID:      int64(seqID),
		TimeoutType: int(timeoutType),
		EventID:     eventID,
	}
}

// createUserTimerTask - Creates a user timer task.
func (tb *timerBuilder) createUserTimerTask(expiryTime int64, startedEventID int64) *persistence.UserTimerTask {
	seqID := ConstructTimerKey(expiryTime, tb.seqNumGen.NextSeq())
	return &persistence.UserTimerTask{
		TaskID:  int64(seqID),
		EventID: startedEventID,
	}
}

func (tb *timerBuilder) loadUserTimer(expires int64, task *persistence.UserTimerTask, taskCreated bool) (*timerDetails, bool) {
	return tb.createTimer(expires, task, taskCreated)
}

func (tb *timerBuilder) createTimer(expires int64, task *persistence.UserTimerTask, taskCreated bool) (*timerDetails, bool) {
	seqNum := tb.localSeqNumGen.NextSeq()
	timer := &timerDetails{
		SequenceID:  ConstructTimerKey(expires, seqNum),
		TimerTask:   task,
		TaskCreated: taskCreated}
	isFirst := tb.insertTimer(timer)
	tb.logger.Debugf("createTimer: td: %s \n", timer)
	return timer, isFirst
}

func (tb *timerBuilder) insertTimer(td *timerDetails) bool {
	size := len(tb.timers)
	i := sort.Search(size,
		func(i int) bool { return tb.timers[i].SequenceID >= td.SequenceID })
	if i == size {
		tb.timers = append(tb.timers, td)
	} else {
		tb.timers = append(tb.timers[:i], append(timers{td}, tb.timers[i:]...)...)
	}
	return i == 0 // This is the first timer in the list.
}

func (tb *timerBuilder) firstTimer() persistence.Task {
	if len(tb.timers) > 0 && !tb.timers[0].TaskCreated {
		return tb.createNewTask(tb.timers[0])
	}
	return nil
}

func (tb *timerBuilder) createNewTask(td *timerDetails) persistence.Task {
	task := td.TimerTask

	// Allocate real sequence number
	expiry, _ := DeconstructTimerKey(td.SequenceID)

	// Create a copy of this task.
	switch task.GetType() {
	case persistence.TaskTypeUserTimer:
		userTimerTask := task.(*persistence.UserTimerTask)
		return tb.createUserTimerTask(expiry, userTimerTask.EventID)
	}
	return nil
}