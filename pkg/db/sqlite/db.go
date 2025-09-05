// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	pb "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite" // SQLite driver

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

type TaskWithUser = db.TaskWithUser
type BatchTaskResult = db.BatchTaskResult

const taskColumnsBase = "parent_id, task_id, pool, instance_id, priority, labels, create_time, start_time, finish_time, resolve_time, completed_children, total_children, status, pending_upstreams"

type columnOptions struct {
	hasPayload bool
	hasResult  bool
}

func taskColumns(options columnOptions) string {
	columns := taskColumnsBase
	if options.hasPayload {
		columns += ", payload"
	}
	if options.hasResult {
		columns += ", result"
	}
	return columns
}

var listTaskQuery = `SELECT ` + taskColumns(columnOptions{}) + `
FROM tasks
WHERE parent_id = ? AND task_id > ?
ORDER BY create_time
LIMIT ?`

var searchTaskQuery = `SELECT ` + taskColumns(columnOptions{}) + `
FROM tasks
WHERE labels LIKE ? AND create_time < ?
ORDER BY create_time DESC
LIMIT ?`

type SqliteDatabase struct {
	db     *sql.DB
	notifs chan struct{}
}

func NewSqliteDatabase(dbPath string) (*SqliteDatabase, error) {
	db, err := sql.Open("sqlite", dbPath+
		"?_pragma=journal_mode(WAL)&"+
		"_pragma=busy_timeout(5000)&"+ // ms; avoids immediate SQLITE_BUSY
		"_pragma=synchronous(NORMAL)&"+ // good balance for WAL
		"_pragma=wal_autocheckpoint=1000")
	if err != nil {
		return nil, err
	}

	// Enable foreign keys in SQLite
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, err
	}

	sqliteDB := &SqliteDatabase{
		db:     db,
		notifs: make(chan struct{}, 1),
	}

	if err := sqliteDB.Init(); err != nil {
		return nil, err
	}

	return sqliteDB, nil
}

func (s *SqliteDatabase) Close() error {
	return s.db.Close()
}

func (s *SqliteDatabase) Init() error {
	// Create tasks table
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tasks(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_id TEXT,
	task_id TEXT NOT NULL,
	instance_id INTEGER,
	create_time TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	labels TEXT, -- JSON array of labels
	pool TEXT NOT NULL,
	priority INTEGER NOT NULL,
	payload BLOB NOT NULL,
	status TEXT NOT NULL,
	lease_by TEXT,
	pending_upstreams INTEGER,
	total_children INTEGER,
	completed_children INTEGER NOT NULL DEFAULT 0,
	start_time TEXT DEFAULT NULL,
	finish_time TEXT DEFAULT NULL,
	resolve_time TEXT DEFAULT NULL,
	result BLOB,
	UNIQUE(parent_id, task_id)
)`)
	if err != nil {
		return fmt.Errorf("failed to create tasks table: %w", err)
	}

	// Create indexes
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_queue ON tasks(pool, priority) WHERE status = 'QUEUEING' AND pending_upstreams = 0`)
	if err != nil {
		return fmt.Errorf("failed to create task queue index: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_lease ON tasks(pool, lease_by) WHERE status = 'LEASED'`)
	if err != nil {
		return fmt.Errorf("failed to create task lease index: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_subtasks ON tasks(create_time DESC, parent_id, task_id)`)
	if err != nil {
		return fmt.Errorf("failed to create subtasks index: %w", err)
	}

	// Create leasers table for task polling
	_, err = s.db.Exec(`
CREATE TABLE IF NOT EXISTS leasers(
	id TEXT PRIMARY KEY,
	last_heartbeat TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	if err != nil {
		return fmt.Errorf("failed to create leasers table: %w", err)
	}

	// Create task_dependencies table for tracking dependencies
	_, err = s.db.Exec(`
CREATE TABLE IF NOT EXISTS task_dependencies(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_id TEXT NOT NULL,
	downstream_task_id TEXT NOT NULL,
	upstream_parent_id TEXT NOT NULL,
	upstream_task_id TEXT NOT NULL,
	trigger_type TEXT NOT NULL CHECK(trigger_type IN ('SUCCESS', 'FAILURE', 'ANY'))
)`)
	if err != nil {
		return fmt.Errorf("failed to create task_dependencies table: %w", err)
	}

	// Create indexes for dependencies
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_dependencies_downstream ON task_dependencies(parent_id, downstream_task_id)`)
	if err != nil {
		return fmt.Errorf("failed to create dependencies downstream index: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_dependencies_upstream ON task_dependencies(upstream_parent_id, upstream_task_id)`)
	if err != nil {
		return fmt.Errorf("failed to create dependencies upstream index: %w", err)
	}

	return nil
}

// Helper function to scan a row into TaskWithUser
func rowToTask(row interface {
	Scan(dest ...interface{}) error
}, options columnOptions) (*proto.Task, error) {
	var task proto.Task
	var payload []byte
	var parentId sql.NullString
	var taskId string
	var labels sql.NullString
	var createTime sql.NullString
	var startTime sql.NullString
	var finishTime sql.NullString
	var resolveTime sql.NullString
	var rawResult []byte
	var totalChildren sql.NullInt32
	var completedChildren sql.NullInt32
	var statusStr string
	var pendingUpstreams int

	dest := []interface{}{
		&parentId,
		&taskId,
		&task.Pool,
		&task.InstanceId,
		&task.Priority,
		&labels,
		&createTime,
		&startTime,
		&finishTime,
		&resolveTime,
		&completedChildren,
		&totalChildren,
		&statusStr,
		&pendingUpstreams}

	if options.hasPayload {
		dest = append(dest, &payload)
	}
	if options.hasResult {
		dest = append(dest, &rawResult)
	}

	err := row.Scan(dest...)
	if err != nil {
		return nil, err
	}

	if parentId.Valid {
		taskId = path.Join(parentId.String, taskId)
	}
	task.Id = taskId

	if createTime.Valid {
		if t, err := time.Parse(time.RFC3339Nano, createTime.String); err == nil {
			task.CreatedAt = timestamppb.New(t)
		}
	}
	if startTime.Valid {
		if t, err := time.Parse(time.RFC3339Nano, startTime.String); err == nil {
			task.StartedAt = timestamppb.New(t)
		}
	}
	if finishTime.Valid {
		if t, err := time.Parse(time.RFC3339Nano, finishTime.String); err == nil {
			task.FinishedAt = timestamppb.New(t)
		}
	}
	if resolveTime.Valid {
		if t, err := time.Parse(time.RFC3339Nano, resolveTime.String); err == nil {
			task.ResolvedAt = timestamppb.New(t)
		}
	}

	if totalChildren.Valid {
		task.ChildrenCount = totalChildren.Int32
	}
	if completedChildren.Valid {
		task.CompletedChildrenCount = completedChildren.Int32
	}

	// Map status strings to proto enums
	switch statusStr {
	case "QUEUEING":
		if pendingUpstreams > 0 {
			task.Status = proto.TaskStatus_TASK_STATUS_PENDING
		} else {
			task.Status = proto.TaskStatus_TASK_STATUS_QUEUEING
		}
	case "LEASED":
		task.Status = proto.TaskStatus_TASK_STATUS_QUEUEING
	case "RUNNING_SUBTASKS":
		task.Status = proto.TaskStatus_TASK_STATUS_RUNNING_SUBTASKS
	case "COMPLETED":
		task.Status = proto.TaskStatus_TASK_STATUS_SUCCESS
	case "FAILED":
		task.Status = proto.TaskStatus_TASK_STATUS_FAILURE
	case "FAILED_UPSTREAM":
		task.Status = proto.TaskStatus_TASK_STATUS_FAILED_UPSTREAM
	}

	// Parse labels if present (stored as JSON array string)
	if labels.Valid && labels.String != "" {
		// Simple parsing for labels - in production you might want proper JSON parsing
		task.Labels = strings.Split(labels.String, ",")
	}

	// Unmarshal payload if present
	if options.hasPayload && len(payload) > 0 {
		task.Workload = &proto.Workload{}
		err = pb.Unmarshal(payload, task.Workload)
		if err != nil {
			return nil, err
		}
	}

	// Unmarshal result if present
	if len(rawResult) > 0 {
		task.BatchTaskResult = &proto.BatchTaskResult{}
		err = pb.Unmarshal(rawResult, task.BatchTaskResult)
		if err != nil {
			return nil, err
		}
	}

	return &task, nil
}

func (s *SqliteDatabase) CreateTask(ctx context.Context, session *proto.SessionRequest) (string, int, error) {
	parentId, taskId := path.Split(session.TaskId)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	status := "QUEUEING"
	upstreamCnt := len(session.Dependencies)

	// Handle dependencies
	if len(session.Dependencies) > 0 {
		downstreamIds := make([]string, 0, len(session.Dependencies))
		types := make([]string, 0, len(session.Dependencies))
		for _, dep := range session.Dependencies {
			downstreamIds = append(downstreamIds, dep.UpstreamTaskId)
			switch dep.Type {
			case proto.Dependency_DEPENDENCY_TYPE_SUCCESS:
				types = append(types, "SUCCESS")
			case proto.Dependency_DEPENDENCY_TYPE_FAILURE:
				types = append(types, "FAILURE")
			default:
				types = append(types, "ANY")
			}
		}
		met, err := s.addDownstreams(ctx, tx, session.TaskId, downstreamIds, types)
		if err != nil {
			if err.Error() == "already failed" {
				status = "FAILED_UPSTREAM"
			} else {
				return "", 0, err
			}
		}
		upstreamCnt -= met
	}

	payload, err := pb.Marshal(session.Workload)
	if err != nil {
		return "", 0, err
	}

	// Convert labels to comma-separated string for simple storage
	labelsStr := ""
	if len(session.Labels) > 0 {
		labelsStr = strings.Join(session.Labels, ",")
	}

	taskIdPrefix := taskId
	nextIndex := int32(2)
	done := false

	for !done {
		result, err := tx.ExecContext(ctx, `
INSERT INTO tasks (parent_id, task_id, pool, instance_id, priority, payload, labels, status, pending_upstreams, create_time)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(parent_id, task_id) DO NOTHING
`, parentId, taskId, session.Pool, session.InstanceId, session.Priority, payload, labelsStr, status, upstreamCnt)

		if err != nil {
			return "", 0, err
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return "", 0, err
		}

		if rowsAffected == 0 {
			// Insert failed due to conflict, generate new task ID
			taskId = fmt.Sprintf("%s_%d", taskIdPrefix, nextIndex)
			nextIndex++
		} else {
			done = true
		}
	}

	session.TaskId = path.Join(parentId, taskId)
	fullTaskId := path.Join(parentId, taskId)

	// Mark pool for polling if task is ready to be queued
	if status == "QUEUEING" && upstreamCnt == 0 {
		err = s.notifyPollReady()
		if err != nil {
			log.Printf("Warning: failed to mark pool for polling: %v", err)
		}
	}

	return fullTaskId, upstreamCnt, tx.Commit()
}

func (s *SqliteDatabase) GetTask(ctx context.Context, taskId string) (*proto.Task, error) {
	parentId, taskId := path.Split(taskId)
	option := columnOptions{hasPayload: true, hasResult: true}
	row := s.db.QueryRowContext(ctx, `
SELECT `+taskColumns(option)+`
FROM tasks
WHERE parent_id = ? AND task_id = ?`, parentId, taskId)
	return rowToTask(row, option)
}

func (s *SqliteDatabase) ListTasks(ctx context.Context, request *proto.ListTasksRequest) ([]*proto.Task, string, error) {
	if request.ParentId != "" && !strings.HasSuffix(request.ParentId, "/") {
		request.ParentId += "/"
	}

	var tasks []*proto.Task
	pageSize := int(request.PageSize)
	if pageSize <= 0 {
		pageSize = 50
	}

	startTask := request.PageToken
	rows, err := s.db.QueryContext(ctx, listTaskQuery, request.ParentId, startTask, pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var nextCursor string
	for rows.Next() {
		task, err := rowToTask(rows, columnOptions{})
		if err != nil {
			return nil, "", err
		}
		tasks = append(tasks, task)
		if len(tasks) == pageSize {
			nextCursor = path.Base(task.Id)
			break
		}
	}

	return tasks, nextCursor, nil
}

func (s *SqliteDatabase) SearchTasks(ctx context.Context, request *proto.SearchTasksRequest) ([]*proto.Task, string, error) {
	var tasks []*proto.Task
	pageSize := int(request.PageSize)
	if pageSize <= 0 {
		pageSize = 50
	}

	labelFilter := ""
	if len(request.GetLabelFilters()) > 0 {
		// Simple label filtering - in production you might want more sophisticated matching
		labelFilter = "%" + request.GetLabelFilters()[0] + "%"
	}

	var startTime time.Time
	if request.PageToken == "" {
		startTime = time.Now().Add(time.Hour * 24)
	} else {
		// Simple cursor decoding
		startTime = time.Now()
	}

	rows, err := s.db.QueryContext(ctx, searchTaskQuery, labelFilter, startTime.Format(time.RFC3339Nano), pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var nextCursor string
	for rows.Next() {
		task, err := rowToTask(rows, columnOptions{})
		if err != nil {
			return nil, "", err
		}
		tasks = append(tasks, task)
		if len(tasks) == pageSize {
			nextCursor = path.Base(task.Id)
			break
		}
	}

	return tasks, nextCursor, nil
}

func (s *SqliteDatabase) addDownstreams(ctx context.Context, tx *sql.Tx, downstreamId string, shortUpstreamIds []string, triggerTypes []string) (int, error) {
	if len(shortUpstreamIds) != len(triggerTypes) {
		return 0, fmt.Errorf("shortUpstreamIds and triggerTypes must have the same length")
	}

	downstreamParentId, downstreamTaskId := path.Split(downstreamId)
	triggerMet := 0

	for i, upstreamId := range shortUpstreamIds {
		upstreamParentId, upstreamTaskId := path.Split(upstreamId)
		triggerType := triggerTypes[i]

		// Insert the dependency relationship
		_, err := tx.ExecContext(ctx, `
INSERT INTO task_dependencies (parent_id, downstream_task_id, upstream_parent_id, upstream_task_id, trigger_type)
VALUES (?, ?, ?, ?, ?)`,
			downstreamParentId, downstreamTaskId, upstreamParentId, upstreamTaskId, triggerType)
		if err != nil {
			return 0, fmt.Errorf("failed to insert dependency: %w", err)
		}

		// Check if upstream task is already completed and if trigger condition is met
		var status string
		err = tx.QueryRowContext(ctx, `
SELECT status FROM tasks WHERE parent_id = ? AND task_id = ?`,
			upstreamParentId, upstreamTaskId).Scan(&status)
		if err != nil {
			if err == sql.ErrNoRows {
				// Upstream task doesn't exist yet, dependency not met
				continue
			}
			return 0, fmt.Errorf("failed to check upstream task status: %w", err)
		}

		// Check if trigger condition is met
		switch status {
		case "COMPLETED":
			if triggerType == "FAILURE" {
				return 0, fmt.Errorf("already failed")
			}
			triggerMet++
		case "FAILED", "FAILED_UPSTREAM":
			if triggerType == "SUCCESS" {
				return 0, fmt.Errorf("already failed")
			}
			triggerMet++
		}
	}

	return triggerMet, nil
}

func (s *SqliteDatabase) notifyPollReady() error {
	select {
	case s.notifs <- struct{}{}:
	default:
	}
	return nil
}

func (s *SqliteDatabase) UpdateTaskFinalResult(ctx context.Context, taskId string, result *BatchTaskResult) error {
	fullTaskId := taskId
	parentId, taskId := path.Split(taskId)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	outcome := "success"
	if result.Payload.TerminatedSignal != 0 || result.Payload.ExitCode == nil || *result.Payload.ExitCode != 0 {
		outcome = "fail"
	}

	var payload []byte
	if payload, err = pb.Marshal(result.Payload); err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339Nano)
	startTimeStr := result.StartTime.Format(time.RFC3339Nano)

	// Update task with result and timing information
	var totalChildren int
	var completedChildren int
	err = tx.QueryRowContext(ctx, `
UPDATE tasks
SET
	total_children = (SELECT COUNT(*) FROM tasks WHERE parent_id = ?),
	status = CASE 
		WHEN (SELECT COUNT(*) FROM tasks WHERE parent_id = ?) > completed_children 
		THEN 'RUNNING_SUBTASKS' 
		ELSE status 
	END,
	start_time = ?,
	finish_time = ?,
	result = ?
WHERE parent_id = ? AND task_id = ?
RETURNING COALESCE(total_children, 0), completed_children`,
		fullTaskId+"/", fullTaskId+"/", startTimeStr, now, payload, parentId, taskId).Scan(&totalChildren, &completedChildren)

	if err != nil {
		return err
	}

	if totalChildren > completedChildren {
		// Not all children are completed. Already updated to RUNNING_SUBTASKS.
		return tx.Commit()
	}

	err = s.updateTaskStatus(ctx, tx, parentId, taskId, outcome)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SqliteDatabase) updateTaskStatus(ctx context.Context, tx *sql.Tx, parentId string, taskId string, outcome string) error {
	for {
		log.Printf("updateTaskStatus: %s%s %s", parentId, taskId, outcome)

		// Use recursive CTE to update downstream tasks
		_, err := tx.ExecContext(ctx, `
WITH RECURSIVE downstream_updates AS (
	-- Base case: the task that just completed
	SELECT ? AS task_id, ? AS parent_id, ? AS outcome

	UNION ALL

	-- Recursive case: find downstream tasks affected by upstream completion
	SELECT 
		deps.downstream_task_id AS task_id,
		deps.parent_id AS parent_id,
		CASE
			WHEN upstream.outcome IN ('fail', 'fail_upstream') AND deps.trigger_type = 'SUCCESS' THEN 'fail_upstream'
			WHEN upstream.outcome = 'success' AND deps.trigger_type = 'FAILURE' THEN 'fail_upstream'
			ELSE 'met'
		END AS outcome
	FROM task_dependencies deps
	JOIN downstream_updates upstream ON 
		deps.upstream_parent_id = upstream.parent_id AND 
		deps.upstream_task_id = upstream.task_id
	JOIN tasks t ON 
		t.parent_id = deps.parent_id AND 
		t.task_id = deps.downstream_task_id
	WHERE t.status IN ('QUEUEING', 'LEASED', 'RUNNING_SUBTASKS') AND upstream.outcome != 'met'
),
downstream_counts AS (
	SELECT
		task_id,
		parent_id,
		-- Use first non-'met' outcome, or NULL if all are 'met'
		(SELECT outcome FROM downstream_updates du2 
		 WHERE du2.task_id = downstream_updates.task_id 
		   AND du2.parent_id = downstream_updates.parent_id 
		   AND outcome != 'met' LIMIT 1) AS outcome,
		COUNT(*) FILTER (WHERE outcome IN ('met', 'fail_upstream')) AS upstream_met
	FROM downstream_updates
	GROUP BY task_id, parent_id
)
UPDATE tasks
SET
	status = CASE
		WHEN downstream_counts.outcome = 'success' THEN 'COMPLETED'
		WHEN downstream_counts.outcome = 'fail' THEN 'FAILED'
		WHEN downstream_counts.outcome = 'fail_upstream' THEN 'FAILED_UPSTREAM'
		ELSE status 
	END,
	pending_upstreams = pending_upstreams - COALESCE(downstream_counts.upstream_met, 0),
	resolve_time = CASE
		WHEN downstream_counts.outcome IN ('success', 'fail', 'fail_upstream') 
		THEN COALESCE(resolve_time, datetime('now'))
		ELSE resolve_time 
	END
FROM downstream_counts
WHERE tasks.parent_id = downstream_counts.parent_id AND tasks.task_id = downstream_counts.task_id
`, taskId, parentId, outcome)

		if err != nil {
			return fmt.Errorf("failed to update downstream tasks: %w", err)
		}

		// Mark pools for polling for tasks that became ready (in-memory)
		rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT pool 
FROM tasks 
WHERE status = 'QUEUEING' AND pending_upstreams = 0`)

		if err != nil {
			log.Printf("Warning: failed to query pools for polling: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var pool string
				if err := rows.Scan(&pool); err == nil {
					s.notifyPollReady()
				}
			}
		}

		// Handle parent task completion if this task has a parent
		if parentId == "" || parentId == "/" {
			break
		}

		// Remove trailing slash and split again to get grandparent info
		cleanParentId := strings.TrimSuffix(parentId, "/")
		if cleanParentId == "" {
			break
		}

		grandParentId, parentTaskId := path.Split(cleanParentId)

		// Update parent task's completed children count
		var completed sql.NullBool
		err = tx.QueryRowContext(ctx, `
UPDATE tasks
SET completed_children = completed_children + 1
WHERE parent_id = ? AND task_id = ?
RETURNING completed_children >= total_children`, grandParentId, parentTaskId).Scan(&completed)

		if err != nil {
			if err == sql.ErrNoRows {
				// Parent task doesn't exist, we're done
				break
			}
			return fmt.Errorf("failed to update parent task %s: %w", cleanParentId, err)
		}

		if !completed.Valid || !completed.Bool {
			// Parent task is not complete yet
			break
		}

		// Check if all sibling tasks are completed successfully
		var failedTasks int
		err = tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM tasks
WHERE parent_id = ? AND status NOT IN ('COMPLETED')`, cleanParentId+"/").Scan(&failedTasks)

		if err != nil {
			return fmt.Errorf("failed to check parent task %s: %w", cleanParentId, err)
		}

		if failedTasks == 0 {
			outcome = "success"
		} else {
			outcome = "fail"
		}

		// Move up to the parent task for the next iteration
		parentId, taskId = grandParentId, parentTaskId
	}
	return nil
}

func (s *SqliteDatabase) pollTasksOnce(ctx context.Context, leaserIdentity string) ([]*TaskWithUser, error) {
	options := columnOptions{hasPayload: true}

	// SQLite doesn't support UPDATE...FROM with complex joins like PostgreSQL
	// We need to use a different approach
	rows, err := s.db.QueryContext(ctx, `
WITH available_tasks AS (
	SELECT parent_id, task_id, pool, instance_id, priority, labels, 
		   create_time, start_time, finish_time, resolve_time, 
		   completed_children, total_children, status, pending_upstreams, payload
	FROM tasks 
	WHERE status = 'QUEUEING' AND pending_upstreams = 0
	ORDER BY priority DESC, create_time ASC
	LIMIT 10
)
UPDATE tasks 
SET status = 'LEASED', lease_by = ?
WHERE (parent_id, task_id) IN (SELECT parent_id, task_id FROM available_tasks)
RETURNING `+taskColumns(options), leaserIdentity)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*TaskWithUser
	for rows.Next() {
		task, err := rowToTask(rows, options)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, &db.TaskWithUser{
			Task: task,
			User: rbac.EmptyUser{},
		})
	}
	return tasks, nil
}

// Task polling with trigger-based and interval-based mechanisms
func (s *SqliteDatabase) PollTasks(
	ctx context.Context,
	leaserIdentity string,
	callback func(leaserIdentity string, task *TaskWithUser) error) error {

	// Set up periodic polling with configurable intervals
	pollInterval := 60 * time.Second // Check for new tasks every 60 seconds

	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	// Helper function to poll and process tasks
	pollAndProcess := func(reason string) error {
		tasks, err := s.pollTasksOnce(ctx, leaserIdentity)
		if err != nil {
			return fmt.Errorf("failed to poll tasks (%s): %w", reason, err)
		}

		if len(tasks) > 0 {
			log.Printf("Found %d tasks to process (%s)", len(tasks), reason)
		}

		for _, task := range tasks {
			if err := callback(leaserIdentity, task); err != nil {
				return fmt.Errorf("callback error for task %s: %w", task.Task.Id, err)
			}
		}
		return nil
	}

	// Initial poll to get any immediately available tasks
	if err := pollAndProcess("initial"); err != nil {
		return err
	}

	// Main polling loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-s.notifs:
			// triggered by markPoolForPolling
			if err := pollAndProcess("triggered"); err != nil {
				return fmt.Errorf("error in triggered poll: %w", err)
			}

		case <-pollTicker.C:
			// Periodic polling as fallback
			if err := pollAndProcess("periodic"); err != nil {
				log.Printf("Error in periodic poll: %v", err)
			}
		}
	}
}

func (s *SqliteDatabase) RenewLeaser(ctx context.Context, leaserIdentity string, now time.Time) error {
	nowStr := now.Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO leasers(id, last_heartbeat)
VALUES(?, ?)
ON CONFLICT(id) DO UPDATE SET last_heartbeat = ?`,
		leaserIdentity, nowStr, nowStr)
	return err
}

func (s *SqliteDatabase) ReconnectTask(ctx context.Context, taskId string, leaserIdentity string) error {
	parentId, taskId := path.Split(taskId)
	result, err := s.db.ExecContext(ctx, `
UPDATE tasks
SET lease_by = ?
WHERE parent_id = ? AND task_id = ? AND status = 'LEASED'`,
		leaserIdentity, parentId, taskId)

	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return fmt.Errorf("task %s not found or not leased", taskId)
	}
	return err
}

func (s *SqliteDatabase) ReleaseExpiredLeaser(ctx context.Context, lastLeaseTime time.Time) error {
	cutoffTime := lastLeaseTime.Format(time.RFC3339Nano)

	// Release expired leased tasks and get the pools that were affected
	rows, err := s.db.QueryContext(ctx, `
UPDATE tasks
SET status = 'QUEUEING', lease_by = NULL
WHERE status = 'LEASED' 
  AND lease_by IN (
    SELECT id FROM leasers WHERE last_heartbeat < ?
  )
RETURNING pool`, cutoffTime)

	if err != nil {
		return err
	}
	defer rows.Close()

	// Collect affected pools and mark them for polling
	poolsToNotify := make(map[string]bool)
	rowCount := 0
	for rows.Next() {
		var pool string
		if err := rows.Scan(&pool); err != nil {
			return err
		}
		poolsToNotify[pool] = true
		rowCount++
	}

	if rowCount > 0 {
		log.Printf("Released %d tasks from expired leasers", rowCount)

		// Mark affected pools for polling
		for pool := range poolsToNotify {
			if err := s.notifyPollReady(); err != nil {
				log.Printf("Warning: failed to mark pool %s for polling: %v", pool, err)
			}
		}
	}

	// Delete expired leasers
	_, err = s.db.ExecContext(ctx, `
DELETE FROM leasers WHERE last_heartbeat < ?`, cutoffTime)

	return err
}

func (s *SqliteDatabase) RunMaintenances(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ReleaseExpiredLeaser(ctx, time.Now().Add(-time.Minute))
		}
	}
}
