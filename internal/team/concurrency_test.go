package team

import (
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupConcurrencyStore creates a temporary Store and Paths for testing.
func setupConcurrencyStore(t *testing.T) (*Store, *Paths) {
	t.Helper()
	dir := t.TempDir()
	paths := NewPaths(dir)
	store := NewStore(paths)
	return store, paths
}

// TestConcurrentTaskClaiming tests that multiple workers can claim tasks concurrently
// without race conditions or duplicate claims.
func TestConcurrentTaskClaiming(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	registry := NewTaskRegistry(store, paths, 10)
	teamName := "test-team"

	// Setup: Create team and 10 tasks
	require.NoError(t, store.EnsureDirs(teamName))

	for i := 1; i <= 10; i++ {
		task := &types.TeamTask{
			ID:      "task-" + string(rune('0'+i)),
			Subject: "Test task",
		}
		require.NoError(t, registry.CreateTask(teamName, task))
	}

	// Spawn 5 workers, each claiming tasks until none remain
	numWorkers := 5
	var wg sync.WaitGroup
	claimedMu := sync.Mutex{}
	claimedTasks := make(map[string]bool)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID string) {
			defer wg.Done()
			for {
				task, token, err := registry.ClaimNextTask(teamName, workerID)
				if err != nil {
					// No more claimable tasks
					return
				}
				if task == nil || token == "" {
					return
				}

				claimedMu.Lock()
				claimedTasks[task.ID] = true
				claimedMu.Unlock()
			}
		}("worker-" + string(rune('0'+w)))
	}

	wg.Wait()

	// Verify: exactly 10 unique tasks claimed
	assert.Equal(t, 10, len(claimedTasks), "should have claimed exactly 10 tasks")

	// Verify: all tasks are in progress
	tasks, err := registry.ListTasks(teamName)
	require.NoError(t, err)
	for _, task := range tasks {
		assert.Equal(t, types.TaskInProgress, task.Status, "all tasks should be in progress")
		assert.NotNil(t, task.Claim, "all tasks should have a claim")
	}
}

// TestConcurrentClaimSameTask tests that only one worker can claim the same task
// when multiple workers try simultaneously.
func TestConcurrentClaimSameTask(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	registry := NewTaskRegistry(store, paths, 10)
	teamName := "test-team"

	// Setup: Create team and 1 task
	require.NoError(t, store.EnsureDirs(teamName))
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Test task",
	}
	require.NoError(t, registry.CreateTask(teamName, task))

	// Get initial version
	createdTask, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)
	initialVersion := createdTask.Version

	// Synchronization: use a channel to start all goroutines at once
	startChan := make(chan struct{})
	numWorkers := 10
	var wg sync.WaitGroup
	successCount := 0
	successMu := sync.Mutex{}

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID string) {
			defer wg.Done()
			<-startChan // Wait for signal to start

			token, err := registry.ClaimTask(teamName, "task-1", workerID, initialVersion)
			if err == nil && token != "" {
				successMu.Lock()
				successCount++
				successMu.Unlock()
			}
		}("worker-" + string(rune('0'+w)))
	}

	// Signal all goroutines to start simultaneously
	close(startChan)
	wg.Wait()

	// Verify: exactly 1 worker succeeded
	assert.Equal(t, 1, successCount, "exactly one worker should successfully claim the task")

	// Verify: task is in progress with a claim
	claimedTask, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimedTask.Status)
	assert.NotNil(t, claimedTask.Claim)
}

// TestConcurrentMailboxSend tests that multiple workers can send messages
// to the same worker's mailbox concurrently without data loss.
func TestConcurrentMailboxSend(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	mailbox := NewMailbox(store, paths)
	teamName := "test-team"

	// Setup: Create team
	require.NoError(t, store.EnsureDirs(teamName))

	// Spawn 10 goroutines, each sending a unique message to "worker-0"
	numSenders := 10
	var wg sync.WaitGroup

	for s := 0; s < numSenders; s++ {
		wg.Add(1)
		go func(senderID int) {
			defer wg.Done()
			content := "message-from-sender-" + string(rune('0'+senderID))
			err := mailbox.Send(teamName, "sender-"+string(rune('0'+senderID)), "worker-0", content)
			assert.NoError(t, err)
		}(s)
	}

	wg.Wait()

	// Verify: exactly 10 messages in worker-0's mailbox
	messages, err := mailbox.ListPending(teamName, "worker-0")
	require.NoError(t, err)
	assert.Equal(t, 10, len(messages), "should have exactly 10 messages")

	// Verify: all messages are unique
	contentSet := make(map[string]bool)
	for _, msg := range messages {
		contentSet[msg.Content] = true
	}
	assert.Equal(t, 10, len(contentSet), "all messages should have unique content")
}

// TestConcurrentMailboxBroadcast tests that broadcast messages are delivered
// to all workers concurrently without data loss.
func TestConcurrentMailboxBroadcast(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	mailbox := NewMailbox(store, paths)
	teamName := "test-team"

	// Setup: Create team
	require.NoError(t, store.EnsureDirs(teamName))

	// Broadcast a message to 3 workers
	recipients := []string{"worker-0", "worker-1", "worker-2"}
	broadcastContent := "broadcast-message"
	err := mailbox.Broadcast(teamName, "broadcaster", recipients, broadcastContent)
	require.NoError(t, err)

	// Verify: each worker has 1 pending message
	for _, workerID := range recipients {
		messages, err := mailbox.ListPending(teamName, workerID)
		require.NoError(t, err)
		assert.Equal(t, 1, len(messages), "worker %s should have 1 message", workerID)
		assert.Equal(t, broadcastContent, messages[0].Content)
	}

	// Spawn 3 goroutines to drain mailboxes concurrently
	var wg sync.WaitGroup
	drainedMu := sync.Mutex{}
	drainedContent := make(map[string]string)

	for _, workerID := range recipients {
		wg.Add(1)
		go func(wID string) {
			defer wg.Done()
			injection, err := mailbox.DrainPending(teamName, wID)
			assert.NoError(t, err)
			assert.NotEmpty(t, injection)

			drainedMu.Lock()
			drainedContent[wID] = injection
			drainedMu.Unlock()
		}(workerID)
	}

	wg.Wait()

	// Verify: all 3 workers drained their messages
	assert.Equal(t, 3, len(drainedContent), "all 3 workers should have drained messages")
	for _, content := range drainedContent {
		assert.Contains(t, content, broadcastContent)
	}
}

// TestConcurrentOwnershipClaim tests that only one worker can claim a set of files
// when multiple workers try simultaneously.
func TestConcurrentOwnershipClaim(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	ownership := NewOwnershipRegistry(store, paths)
	teamName := "test-team"

	// Setup: Create team
	require.NoError(t, store.EnsureDirs(teamName))

	filesToClaim := []string{"src/main.go", "src/utils.go"}
	numWorkers := 5

	// Synchronization: use a channel to start all goroutines at once
	startChan := make(chan struct{})
	var wg sync.WaitGroup
	resultsMu := sync.Mutex{}
	results := make(map[string]bool) // workerID -> success

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID string) {
			defer wg.Done()
			<-startChan // Wait for signal to start

			conflicts, err := ownership.Claim(teamName, workerID, "task-1", filesToClaim)
			assert.NoError(t, err)

			resultsMu.Lock()
			// Success if no conflicts
			results[workerID] = len(conflicts) == 0
			resultsMu.Unlock()
		}("worker-" + string(rune('0'+w)))
	}

	// Signal all goroutines to start simultaneously
	close(startChan)
	wg.Wait()

	// Verify: exactly 1 worker succeeded without conflicts
	successCount := 0
	var winnerID string
	for workerID, success := range results {
		if success {
			successCount++
			winnerID = workerID
		}
	}
	assert.Equal(t, 1, successCount, "exactly one worker should claim without conflicts")

	// Verify: ListAll shows the files owned by the winning worker
	allOwnership, err := ownership.ListAll(teamName)
	require.NoError(t, err)
	assert.Equal(t, 2, len(allOwnership), "should have 2 file ownership entries")

	for _, entry := range allOwnership {
		assert.Equal(t, winnerID, entry.WorkerID, "all files should be owned by the winning worker")
		assert.Equal(t, "task-1", entry.TaskID)
	}
}

// TestConcurrentOwnershipClaimDisjointFiles tests that multiple workers can claim
// different files concurrently without conflicts.
func TestConcurrentOwnershipClaimDisjointFiles(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	ownership := NewOwnershipRegistry(store, paths)
	teamName := "test-team"

	// Setup: Create team
	require.NoError(t, store.EnsureDirs(teamName))

	numWorkers := 3
	var wg sync.WaitGroup
	resultsMu := sync.Mutex{}
	results := make(map[string]bool) // workerID -> success

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID string, fileChar rune) {
			defer wg.Done()

			// Each worker claims a different file
			filesToClaim := []string{string(fileChar) + ".go"}
			conflicts, err := ownership.Claim(teamName, workerID, "task-1", filesToClaim)
			assert.NoError(t, err)

			resultsMu.Lock()
			results[workerID] = len(conflicts) == 0
			resultsMu.Unlock()
		}("worker-"+string(rune('0'+w)), rune('a'+w))
	}

	wg.Wait()

	// Verify: all workers succeeded
	for workerID, success := range results {
		assert.True(t, success, "worker %s should claim without conflicts", workerID)
	}

	// Verify: ListAll shows 3 files, each owned by different workers
	allOwnership, err := ownership.ListAll(teamName)
	require.NoError(t, err)
	assert.Equal(t, 3, len(allOwnership), "should have 3 file ownership entries")

	workerSet := make(map[string]bool)
	for _, entry := range allOwnership {
		workerSet[entry.WorkerID] = true
	}
	assert.Equal(t, 3, len(workerSet), "files should be owned by 3 different workers")
}

// TestConcurrentLeaseRenewal tests that a task lease can be renewed concurrently
// while the main goroutine verifies the task remains claimed.
func TestConcurrentLeaseRenewal(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	registry := NewTaskRegistry(store, paths, 1) // 1 second lease
	teamName := "test-team"

	// Setup: Create team and task
	require.NoError(t, store.EnsureDirs(teamName))
	task := &types.TeamTask{
		ID:      "task-1",
		Subject: "Test task",
	}
	require.NoError(t, registry.CreateTask(teamName, task))

	// Claim the task
	createdTask, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)
	token, err := registry.ClaimTask(teamName, "task-1", "worker-0", createdTask.Version)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Spawn a goroutine that renews the lease every 100ms
	stopRenewal := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopRenewal:
				return
			case <-ticker.C:
				_ = registry.RenewLease(teamName, "task-1", token)
			}
		}
	}()

	// Main goroutine: sleep 500ms then verify task is still claimed
	time.Sleep(500 * time.Millisecond)

	claimedTask, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimedTask.Status, "task should still be in progress after 500ms with renewal")
	assert.NotNil(t, claimedTask.Claim, "task should still have a claim")

	// Stop renewal and wait for goroutine
	close(stopRenewal)
	wg.Wait()

	// Sleep past lease expiry (1 second) without renewal
	time.Sleep(1200 * time.Millisecond)

	// Verify: task becomes claimable again (lease expired)
	expiredTask, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskPending, expiredTask.Status, "task should be pending after lease expiry")
	assert.Nil(t, expiredTask.Claim, "task should have no claim after lease expiry")
}
