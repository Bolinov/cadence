package persistence

import (
	"math"
	"os"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	gen "github.com/uber/cadence/.gen/go/shared"
	"github.com/uber/cadence/common"
)

type (
	cassandraPersistenceSuite struct {
		suite.Suite
		TestBase
		// override suite.Suite.Assertions with require.Assertions; this means that s.NotNil(nil) will stop the test,
		// not merely log an error
		*require.Assertions
	}
)

func TestCassandraPersistenceSuite(t *testing.T) {
	s := new(cassandraPersistenceSuite)
	suite.Run(t, s)
}

func (s *cassandraPersistenceSuite) SetupSuite() {
	if testing.Verbose() {
		log.SetOutput(os.Stdout)
	}

	s.SetupWorkflowStore()
}

func (s *cassandraPersistenceSuite) TearDownSuite() {
	s.TearDownWorkflowStore()
}

func (s *cassandraPersistenceSuite) SetupTest() {
	// Have to define our overridden assertions in the test setup. If we did it earlier, s.T() will return nil
	s.Assertions = require.New(s.T())
	s.ClearTransferQueue()
}

func (s *cassandraPersistenceSuite) TestPersistenceStartWorkflow() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("start-workflow-test"),
		RunId: common.StringPtr("7f9fe8a0-9237-11e6-ae22-56b6b6499611")}
	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "queue1", "event1", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	task1, err1 := s.CreateWorkflowExecution(workflowExecution, "queue1", "event1", nil, 3, 0, 2, nil)
	s.NotNil(err1, "Expected workflow creation to fail.")
	log.Infof("Unable to start workflow execution: %v", err1)
	s.IsType(&gen.WorkflowExecutionAlreadyStartedError{}, err1)
	s.Empty(task1, "Expected empty task identifier.")

	response, err2 := s.WorkflowMgr.CreateWorkflowExecution(&CreateWorkflowExecutionRequest{
		Execution:          workflowExecution,
		TaskList:           "queue1",
		History:            []byte("event1"),
		ExecutionContext:   nil,
		NextEventID:        int64(3),
		LastProcessedEvent: 0,
		RangeID:            s.ShardContext.GetRangeID() - 1,
		TransferTasks: []Task{
			&DecisionTask{TaskID: s.GetNextSequenceNumber(), TaskList: "queue1", ScheduleID: int64(2)},
		},
		TimerTasks: nil})

	s.NotNil(err2, "Expected workflow creation to fail.")
	s.Nil(response)
	log.Infof("Unable to start workflow execution: %v", err1)
	s.IsType(&ShardOwnershipLostError{}, err2)
}

func (s *cassandraPersistenceSuite) TestGetWorkflow() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("get-workflow-test"),
		RunId: common.StringPtr("918e7b1d-bfa4-4fe0-86cb-604858f90ce4")}
	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "queue1", "event1", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	info, err1 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(info, "Valid Workflow response expected.")
	s.NotNil(info, "Valid Workflow info expected.")
	s.Equal("get-workflow-test", info.WorkflowID)
	s.Equal("918e7b1d-bfa4-4fe0-86cb-604858f90ce4", info.RunID)
	s.Equal("queue1", info.TaskList)
	s.Equal("event1", string(info.History))
	s.Equal([]byte(nil), info.ExecutionContext)
	s.Equal(WorkflowStateCreated, info.State)
	s.Equal(int64(3), info.NextEventID)
	s.Equal(int64(0), info.LastProcessedEvent)
	s.Equal(true, info.DecisionPending)
	s.Equal(true, validateTimeRange(info.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info.LastUpdatedTimestamp)
}

func (s *cassandraPersistenceSuite) TestUpdateWorkflow() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("update-workflow-test"),
		RunId: common.StringPtr("5ba5e531-e46b-48d9-b4b3-859919839553")}
	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "queue1", "event1", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	info0, err1 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(info0, "Valid Workflow info expected.")
	s.Equal("update-workflow-test", info0.WorkflowID)
	s.Equal("5ba5e531-e46b-48d9-b4b3-859919839553", info0.RunID)
	s.Equal("queue1", info0.TaskList)
	s.Equal("event1", string(info0.History))
	s.Equal([]byte(nil), info0.ExecutionContext)
	s.Equal(WorkflowStateCreated, info0.State)
	s.Equal(int64(3), info0.NextEventID)
	s.Equal(int64(0), info0.LastProcessedEvent)
	s.Equal(true, info0.DecisionPending)
	s.Equal(true, validateTimeRange(info0.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info0.LastUpdatedTimestamp)

	updatedInfo := copyWorkflowExecutionInfo(info0)
	updatedInfo.History = []byte(`event2`)
	updatedInfo.NextEventID = int64(5)
	updatedInfo.LastProcessedEvent = int64(2)
	err2 := s.UpdateWorkflowExecution(updatedInfo, []int64{int64(4)}, nil, int64(3), nil, nil, nil, nil, nil, nil)
	s.Nil(err2, "No error expected.")

	info1, err3 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err3, "No error expected.")
	s.NotNil(info1, "Valid Workflow info expected.")
	s.Equal("update-workflow-test", info1.WorkflowID)
	s.Equal("5ba5e531-e46b-48d9-b4b3-859919839553", info1.RunID)
	s.Equal("queue1", info1.TaskList)
	s.Equal("event2", string(info1.History))
	s.Equal([]byte(nil), info1.ExecutionContext)
	s.Equal(WorkflowStateCreated, info1.State)
	s.Equal(int64(5), info1.NextEventID)
	s.Equal(int64(2), info1.LastProcessedEvent)
	s.Equal(true, info1.DecisionPending)
	s.Equal(true, validateTimeRange(info1.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info1.LastUpdatedTimestamp)

	failedUpdatedInfo := copyWorkflowExecutionInfo(info0)
	failedUpdatedInfo.History = []byte(`event3`)
	failedUpdatedInfo.NextEventID = int64(6)
	failedUpdatedInfo.LastProcessedEvent = int64(3)
	err4 := s.UpdateWorkflowExecution(updatedInfo, []int64{int64(5)}, nil, int64(3), nil, nil, nil, nil, nil, nil)
	s.NotNil(err4, "expected non nil error.")
	s.IsType(&ConditionFailedError{}, err4)
	log.Errorf("Conditional update failed with error: %v", err4)

	info2, err4 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err4, "No error expected.")
	s.NotNil(info2, "Valid Workflow info expected.")
	s.Equal("update-workflow-test", info2.WorkflowID)
	s.Equal("5ba5e531-e46b-48d9-b4b3-859919839553", info2.RunID)
	s.Equal("queue1", info2.TaskList)
	s.Equal("event2", string(info2.History))
	s.Equal([]byte(nil), info2.ExecutionContext)
	s.Equal(WorkflowStateCreated, info2.State)
	s.Equal(int64(5), info2.NextEventID)
	s.Equal(int64(2), info2.LastProcessedEvent)
	s.Equal(true, info2.DecisionPending)
	s.Equal(true, validateTimeRange(info2.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info2.LastUpdatedTimestamp)

	failedUpdatedInfo2 := copyWorkflowExecutionInfo(info1)
	failedUpdatedInfo2.History = []byte(`event4`)
	failedUpdatedInfo2.NextEventID = int64(6)
	failedUpdatedInfo2.LastProcessedEvent = int64(3)
	err5 := s.UpdateWorkflowExecutionWithRangeID(updatedInfo, []int64{int64(5)}, nil, int64(12345), int64(5), nil, nil, nil, nil, nil, nil)
	s.NotNil(err5, "expected non nil error.")
	s.IsType(&ShardOwnershipLostError{}, err5)
	log.Errorf("Conditional update failed with error: %v", err5)

	info3, err6 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err6, "No error expected.")
	s.NotNil(info3, "Valid Workflow info expected.")
	s.Equal("update-workflow-test", info3.WorkflowID)
	s.Equal("5ba5e531-e46b-48d9-b4b3-859919839553", info3.RunID)
	s.Equal("queue1", info3.TaskList)
	s.Equal("event2", string(info3.History))
	s.Equal([]byte(nil), info3.ExecutionContext)
	s.Equal(WorkflowStateCreated, info3.State)
	s.Equal(int64(5), info3.NextEventID)
	s.Equal(int64(2), info3.LastProcessedEvent)
	s.Equal(true, info3.DecisionPending)
	s.Equal(true, validateTimeRange(info3.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info3.LastUpdatedTimestamp)
}

func (s *cassandraPersistenceSuite) TestDeleteWorkflow() {
	workflowExecution := gen.WorkflowExecution{
		WorkflowId: common.StringPtr("delete-workflow-test"),
		RunId:      common.StringPtr("4e0917f2-9361-4a14-b16f-1fafe09b287a")}
	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "queue1", "event1", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotNil(task0, "Expected non empty task identifier.")

	info0, err1 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(info0, "Valid Workflow info expected.")
	s.Equal("delete-workflow-test", info0.WorkflowID)
	s.Equal("4e0917f2-9361-4a14-b16f-1fafe09b287a", info0.RunID)
	s.Equal("queue1", info0.TaskList)
	s.Equal("event1", string(info0.History))
	s.Equal([]byte(nil), info0.ExecutionContext)
	s.Equal(WorkflowStateCreated, info0.State)
	s.Equal(int64(3), info0.NextEventID)
	s.Equal(int64(0), info0.LastProcessedEvent)
	s.Equal(true, info0.DecisionPending)
	s.Equal(true, validateTimeRange(info0.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info0.LastUpdatedTimestamp)

	err4 := s.DeleteWorkflowExecution(info0)
	s.Nil(err4, "No error expected.")

	info1, err3 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err3, "No error expected.")
	s.NotNil(info1, "Valid Workflow info expected.")
	s.Equal("delete-workflow-test", info1.WorkflowID)
	s.Equal("4e0917f2-9361-4a14-b16f-1fafe09b287a", info1.RunID)
	s.Equal("queue1", info1.TaskList)
	s.Equal("event1", string(info1.History))
	s.Equal([]byte(nil), info1.ExecutionContext)
	s.Equal(WorkflowStateCreated, info1.State)
	s.Equal(int64(3), info1.NextEventID)
	s.Equal(int64(0), info1.LastProcessedEvent)
	s.Equal(true, info1.DecisionPending)
	s.Equal(true, validateTimeRange(info1.LastUpdatedTimestamp, time.Hour))
	log.Infof("Workflow execution last updated: %v", info1.LastUpdatedTimestamp)

	err5 := s.DeleteWorkflowExecution(info1)
	s.Nil(err5, "No error expected.")
}

func (s *cassandraPersistenceSuite) TestTransferTasks() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("get-transfer-tasks-test"),
		RunId: common.StringPtr("93c87aff-ed89-4ecb-b0fd-d5d1e25dc46d")}

	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "queue1", "event1", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	tasks1, err1 := s.GetTransferTasks(1)
	s.Nil(err1, "No error expected.")
	s.NotNil(tasks1, "expected valid list of tasks.")
	s.Equal(1, len(tasks1), "Expected 1 decision task.")
	task1 := tasks1[0]
	s.Equal(workflowExecution.GetWorkflowId(), task1.WorkflowID)
	s.Equal(workflowExecution.GetRunId(), task1.RunID)
	s.Equal("queue1", task1.TaskList)
	s.Equal(TaskListTypeDecision, task1.TaskType)
	s.Equal(int64(2), task1.ScheduleID)

	err3 := s.CompleteTransferTask(workflowExecution, task1.TaskID)
	s.Nil(err3)

	err4 := s.CompleteTransferTask(workflowExecution, task1.TaskID)
	s.NotNil(err4)
	log.Infof("Failed to complete task '%v'.  Error: %v", task1.TaskID, err4)
	s.IsType(&gen.EntityNotExistsError{}, err4)
}

func (s *cassandraPersistenceSuite) TestCreateTask() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("create-task-test"),
		RunId: common.StringPtr("c949447a-691a-4132-8b2a-a5b38106793c")}
	task0, err0 := s.CreateDecisionTask(workflowExecution, "a5b38106793c", 5)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	tasks1, err1 := s.CreateActivityTasks(workflowExecution, map[int64]string{
		10: "a5b38106793c"})
	s.Nil(err1, "No error expected.")
	s.NotNil(tasks1, "Expected valid task identifiers.")
	s.Equal(1, len(tasks1), "expected single valid task identifier.")
	for _, t := range tasks1 {
		s.NotEmpty(t, "Expected non empty task identifier.")
	}

	tasks2, err2 := s.CreateActivityTasks(workflowExecution, map[int64]string{
		20: "a5b38106793a",
		30: "a5b38106793b",
		40: "a5b38106793c",
		50: "a5b38106793d",
		60: "a5b38106793e",
	})
	s.Nil(err2, "No error expected.")
	s.Equal(5, len(tasks2), "expected single valid task identifier.")
	for _, t := range tasks2 {
		s.NotEmpty(t, "Expected non empty task identifier.")
	}
}

func (s *cassandraPersistenceSuite) TestGetDecisionTasks() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("get-decision-task-test"),
		RunId: common.StringPtr("db20f7e2-1a1e-40d9-9278-d8b886738e05")}
	taskList := "d8b886738e05"
	task0, err0 := s.CreateDecisionTask(workflowExecution, taskList, 5)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	tasks1Response, err1 := s.GetTasks(taskList, TaskListTypeDecision, 1)
	s.Nil(err1, "No error expected.")
	s.NotNil(tasks1Response.Tasks, "expected valid list of tasks.")
	s.Equal(1, len(tasks1Response.Tasks), "Expected 1 decision task.")
}

func (s *cassandraPersistenceSuite) TestCompleteDecisionTask() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("complete-decision-task-test"),
		RunId: common.StringPtr("2aa0a74e-16ee-4f27-983d-48b07ec1915d")}
	taskList := "48b07ec1915d"
	tasks0, err0 := s.CreateActivityTasks(workflowExecution, map[int64]string{
		10: taskList,
		20: taskList,
		30: taskList,
		40: taskList,
		50: taskList,
	})
	s.Nil(err0, "No error expected.")
	s.NotNil(tasks0, "Expected non empty task identifier.")
	s.Equal(5, len(tasks0), "expected 5 valid task identifier.")
	for _, t := range tasks0 {
		s.NotEmpty(t, "Expected non empty task identifier.")
	}

	tasksWithID1Response, err1 := s.GetTasks(taskList, TaskListTypeActivity, 5)

	s.Nil(err1, "No error expected.")
	tasksWithID1 := tasksWithID1Response.Tasks
	s.NotNil(tasksWithID1, "expected valid list of tasks.")

	s.Equal(5, len(tasksWithID1), "Expected 5 activity tasks.")
	for _, t := range tasksWithID1 {
		s.Equal(workflowExecution.GetWorkflowId(), t.WorkflowID)
		s.Equal(workflowExecution.GetRunId(), t.RunID)
		s.True(t.TaskID > 0)

		err2 := s.CompleteTask(taskList, TaskListTypeActivity, t.TaskID, 100)
		s.Nil(err2)
	}
}

func (s *cassandraPersistenceSuite) TestLeaseTaskList() {

	taskList := "aaaaaaa"
	response, err := s.TaskMgr.LeaseTaskList(&LeaseTaskListRequest{TaskList: taskList, TaskType: TaskListTypeActivity})
	s.NoError(err)
	tli := response.TaskListInfo
	s.EqualValues(1, tli.RangeID)
	s.EqualValues(0, tli.AckLevel)
	err = s.TaskMgr.CompleteTask(
		&CompleteTaskRequest{TaskID: 110, TaskList: &TaskListInfo{Name: taskList, TaskType: TaskListTypeActivity, AckLevel: 100, RangeID: tli.RangeID}})
	s.NoError(err)

	response, err = s.TaskMgr.LeaseTaskList(&LeaseTaskListRequest{TaskList: taskList, TaskType: TaskListTypeActivity})
	s.NoError(err)
	tli = response.TaskListInfo
	s.EqualValues(2, tli.RangeID)
	s.EqualValues(100, tli.AckLevel)
}

func (s *cassandraPersistenceSuite) TestTimerTasks() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("get-timer-tasks-test"),
		RunId: common.StringPtr("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")}

	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "taskList", "history", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	info0, err1 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(info0, "Valid Workflow info expected.")

	updatedInfo := copyWorkflowExecutionInfo(info0)
	updatedInfo.History = []byte(`event2`)
	updatedInfo.NextEventID = int64(5)
	updatedInfo.LastProcessedEvent = int64(2)
	tasks := []Task{&DecisionTimeoutTask{1, 2}}
	err2 := s.UpdateWorkflowExecution(updatedInfo, []int64{int64(4)}, nil, int64(3), tasks, nil, nil, nil, nil, nil)
	s.Nil(err2, "No error expected.")

	timerTasks, err1 := s.GetTimerIndexTasks(-1, math.MaxInt64)
	s.Nil(err1, "No error expected.")
	s.NotNil(timerTasks, "expected valid list of tasks.")

	err2 = s.UpdateWorkflowExecution(updatedInfo, nil, nil, int64(5), nil, &DecisionTimeoutTask{TaskID: timerTasks[0].TaskID}, nil, nil, nil, nil)
	s.Nil(err2, "No error expected.")

	timerTasks2, err2 := s.GetTimerIndexTasks(-1, math.MaxInt64)
	s.Nil(err2, "No error expected.")
	s.Empty(timerTasks2, "expected empty task list.")
}

func (s *cassandraPersistenceSuite) TestWorkflowMutableState_Activities() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("test-workflow-mutable-test"),
		RunId: common.StringPtr("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")}

	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "taskList", "history", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	info0, err1 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(info0, "Valid Workflow info expected.")

	updatedInfo := copyWorkflowExecutionInfo(info0)
	updatedInfo.History = []byte(`event2`)
	updatedInfo.NextEventID = int64(5)
	updatedInfo.LastProcessedEvent = int64(2)
	activityInfos := []*ActivityInfo{{
		ScheduleID: 1, ScheduleToCloseTimeout: 1, ScheduleToStartTimeout: 2, StartToCloseTimeout: 3, HeartbeatTimeout: 4}}
	err2 := s.UpdateWorkflowExecution(updatedInfo, []int64{int64(4)}, nil, int64(3), nil, nil, activityInfos, nil, nil, nil)
	s.Nil(err2, "No error expected.")

	state, err1 := s.GetWorkflowMutableState(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(state, "expected valid state.")
	s.Equal(1, len(state.ActivitInfos))

	err2 = s.UpdateWorkflowExecution(updatedInfo, nil, nil, int64(5), nil, nil, nil, common.Int64Ptr(1), nil, nil)
	s.Nil(err2, "No error expected.")

	state, err1 = s.GetWorkflowMutableState(workflowExecution)
	s.Nil(err2, "No error expected.")
	s.NotNil(state, "expected valid state.")
	s.Equal(0, len(state.ActivitInfos))
}

func (s *cassandraPersistenceSuite) TestWorkflowMutableState_Timers() {
	workflowExecution := gen.WorkflowExecution{WorkflowId: common.StringPtr("test-workflow-mutable-timers-test"),
		RunId: common.StringPtr("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")}

	task0, err0 := s.CreateWorkflowExecution(workflowExecution, "taskList", "history", nil, 3, 0, 2, nil)
	s.Nil(err0, "No error expected.")
	s.NotEmpty(task0, "Expected non empty task identifier.")

	info0, err1 := s.GetWorkflowExecutionInfo(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(info0, "Valid Workflow info expected.")

	updatedInfo := copyWorkflowExecutionInfo(info0)
	updatedInfo.History = []byte(`event2`)
	updatedInfo.NextEventID = int64(5)
	updatedInfo.LastProcessedEvent = int64(2)
	currentTime := time.Now().UTC()
	timerID := "id_1"
	timerInfos := []*TimerInfo{{TimerID: timerID, ExpiryTime: currentTime, TaskID: 2, StartedID: 5}}
	err2 := s.UpdateWorkflowExecution(updatedInfo, []int64{int64(4)}, nil, int64(3), nil, nil, nil, nil, timerInfos, nil)
	s.Nil(err2, "No error expected.")

	state, err1 := s.GetWorkflowMutableState(workflowExecution)
	s.Nil(err1, "No error expected.")
	s.NotNil(state, "expected valid state.")
	s.Equal(1, len(state.TimerInfos))
	s.Equal(timerID, state.TimerInfos[timerID].TimerID)
	s.Equal(currentTime.Unix(), state.TimerInfos[timerID].ExpiryTime.Unix())
	s.Equal(int64(2), state.TimerInfos[timerID].TaskID)
	s.Equal(int64(5), state.TimerInfos[timerID].StartedID)

	err2 = s.UpdateWorkflowExecution(updatedInfo, nil, nil, int64(5), nil, nil, nil, nil, nil, []string{timerID})
	s.Nil(err2, "No error expected.")

	state, err1 = s.GetWorkflowMutableState(workflowExecution)
	s.Nil(err2, "No error expected.")
	s.NotNil(state, "expected valid state.")
	s.Equal(0, len(state.TimerInfos))
}

func copyWorkflowExecutionInfo(sourceInfo *WorkflowExecutionInfo) *WorkflowExecutionInfo {
	return &WorkflowExecutionInfo{
		WorkflowID:           sourceInfo.WorkflowID,
		RunID:                sourceInfo.RunID,
		TaskList:             sourceInfo.TaskList,
		History:              sourceInfo.History,
		ExecutionContext:     sourceInfo.ExecutionContext,
		State:                sourceInfo.State,
		NextEventID:          sourceInfo.NextEventID,
		LastProcessedEvent:   sourceInfo.LastProcessedEvent,
		LastUpdatedTimestamp: sourceInfo.LastUpdatedTimestamp,
		DecisionPending:      sourceInfo.DecisionPending,
	}
}