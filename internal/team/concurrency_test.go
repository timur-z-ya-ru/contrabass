package team

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func setupConcurrencyStore(t *testing.T) (*Store, *Paths) {
	t.Helper()
	paths := NewPaths(t.TempDir())
	store := NewStore(paths)
	return store, paths
}

func TestHeartbeatMonitor_ConcurrentWrites(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	monitor := NewHeartbeatMonitor(store, paths, 5*time.Minute)
	teamName := "team-concurrency"
	require.NoError(t, store.EnsureDirs(teamName))

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		workerID := fmt.Sprintf("worker-%02d", i)
		wg.Add(1)
		go func(id string, pid int) {
			defer wg.Done()
			errCh <- monitor.Write(teamName, Heartbeat{
				WorkerID:    id,
				PID:         pid,
				CurrentTask: fmt.Sprintf("task-%s", id),
				Status:      "running",
				Timestamp:   time.Now(),
			})
		}(workerID, 1000+i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	all, err := monitor.ListAll(teamName)
	require.NoError(t, err)
	require.Len(t, all, workers)

	seenWorkers := make(map[string]struct{}, workers)
	for _, hb := range all {
		seenWorkers[hb.WorkerID] = struct{}{}
		assert.Equal(t, "running", hb.Status)
		assert.NotZero(t, hb.PID)
		assert.False(t, hb.Timestamp.IsZero())
	}
	assert.Len(t, seenWorkers, workers)
}

func TestTaskRegistry_ConcurrentClaimTaskOptimisticLocking(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	registry := NewTaskRegistry(store, paths, 60)
	teamName := "team-concurrency"
	require.NoError(t, store.EnsureDirs(teamName))
	require.NoError(t, registry.CreateTask(teamName, &types.TeamTask{ID: "task-1", Subject: "claim me"}))

	task, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)

	const contenders = 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	var successCount atomic.Int64
	errCh := make(chan error, contenders)

	for i := 0; i < contenders; i++ {
		workerID := fmt.Sprintf("worker-%02d", i)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			token, claimErr := registry.ClaimTask(teamName, "task-1", id, task.Version)
			if claimErr == nil {
				if token == "" {
					errCh <- errors.New("claim succeeded with empty token")
					return
				}
				successCount.Add(1)
				return
			}

			if !errors.Is(claimErr, ErrVersionConflict) && !errors.Is(claimErr, ErrTaskNotClaimable) {
				errCh <- claimErr
			}
		}(workerID)
	}

	close(start)
	wg.Wait()
	close(errCh)
	for claimErr := range errCh {
		require.NoError(t, claimErr)
	}

	assert.Equal(t, int64(1), successCount.Load())

	claimed, err := registry.GetTask(teamName, "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskInProgress, claimed.Status)
	require.NotNil(t, claimed.Claim)
	assert.NotEmpty(t, claimed.Claim.WorkerID)
}

func TestDispatchQueue_ConcurrentDispatchAndAck(t *testing.T) {
	store, paths := setupConcurrencyStore(t)
	queue := NewDispatchQueue(store, paths, 30*time.Second)
	teamName := "team-concurrency"
	require.NoError(t, store.EnsureDirs(teamName))

	const taskCount = 20
	g, ctx := errgroup.WithContext(context.Background())

	for i := 0; i < taskCount; i++ {
		i := i
		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			return queue.Dispatch(teamName, DispatchEntry{
				TaskID:   fmt.Sprintf("task-%02d", i),
				WorkerID: fmt.Sprintf("worker-%02d", i),
				Prompt:   fmt.Sprintf("run task-%02d", i),
			})
		})
	}
	require.NoError(t, g.Wait())

	var ackWG sync.WaitGroup
	errCh := make(chan error, taskCount)
	for i := 0; i < taskCount; i++ {
		i := i
		ackWG.Add(1)
		go func() {
			defer ackWG.Done()
			errCh <- queue.Ack(teamName, fmt.Sprintf("task-%02d", i), fmt.Sprintf("worker-%02d", i))
		}()
	}
	ackWG.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	entries, err := queue.ListAll(teamName)
	require.NoError(t, err)
	require.Len(t, entries, taskCount)
	for _, entry := range entries {
		assert.Equal(t, DispatchStatusAcked, entry.Status)
		assert.NotNil(t, entry.AckedAt)
	}
}

func TestEventLogger_ConcurrentLogWrites(t *testing.T) {
	_, paths := setupConcurrencyStore(t)
	logger := NewEventLogger(paths)
	teamName := "team-concurrency"

	const eventCount = 40
	g := errgroup.Group{}
	for i := 0; i < eventCount; i++ {
		i := i
		g.Go(func() error {
			return logger.Log(teamName, LoggedEvent{
				Type:     "worker_progress",
				WorkerID: fmt.Sprintf("worker-%02d", i),
				TaskID:   fmt.Sprintf("task-%02d", i),
				Data: map[string]interface{}{
					"seq": i,
				},
			})
		})
	}
	require.NoError(t, g.Wait())

	events, err := logger.Read(teamName, nil)
	require.NoError(t, err)
	require.Len(t, events, eventCount)

	idSet := make(map[string]struct{}, eventCount)
	for _, event := range events {
		idSet[event.ID] = struct{}{}
		assert.Equal(t, teamName, event.TeamName)
		assert.Equal(t, "worker_progress", event.Type)
	}
	assert.Len(t, idSet, eventCount)

	raw, err := os.ReadFile(filepath.Join(paths.EventsDir(teamName), eventsFileName))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, eventCount)
}

func TestStore_ConcurrentManifestUpdatesAtomicWrite(t *testing.T) {
	tests := []struct {
		name       string
		goroutines int
		iterations int
	}{
		{
			name:       "multiple goroutines repeatedly update manifest",
			goroutines: 10,
			iterations: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths := setupConcurrencyStore(t)
			teamName := "team-concurrency"

			_, err := store.CreateManifest(teamName, types.TeamConfig{})
			require.NoError(t, err)

			g := errgroup.Group{}
			for i := 0; i < tt.goroutines; i++ {
				g.Go(func() error {
					for j := 0; j < tt.iterations; j++ {
						if err := store.UpdateManifest(teamName, func(m *types.TeamManifest) error {
							m.Config.MaxWorkers++
							return nil
						}); err != nil {
							return err
						}
					}
					return nil
				})
			}

			require.NoError(t, g.Wait())

			manifest, err := store.LoadManifest(teamName)
			require.NoError(t, err)
			assert.Equal(t, tt.goroutines*tt.iterations, manifest.Config.MaxWorkers)

			var decoded map[string]interface{}
			require.NoError(t, store.ReadJSON(paths.ManifestPath(teamName), &decoded))
			_, statErr := os.Stat(paths.ManifestPath(teamName) + ".tmp")
			assert.True(t, errors.Is(statErr, os.ErrNotExist))
		})
	}
}
