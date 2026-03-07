import './App.css'
import { useEffect, useState } from 'react'
import { Header } from './components/Header'
import { MetricCards } from './components/MetricCards'
import { RateLimits } from './components/RateLimits'
import { RetryQueue } from './components/RetryQueue'
import { SessionsTable } from './components/SessionsTable'
import { TeamTable } from './components/TeamTable'
import { WorkerTable } from './components/WorkerTable'
import { BoardView } from './components/BoardView'
import { AgentLogs } from './components/AgentLogs'
import { useSSE } from './hooks/useSSE'

function computeRuntimeSeconds(startTime: string | undefined): number {
  if (!startTime) {
    return 0
  }

  const start = Date.parse(startTime)
  if (Number.isNaN(start)) {
    return 0
  }

  return Math.max(0, Math.floor((Date.now() - start) / 1000))
}

function App() {
  const { state, connected, error, teamSnapshot, boardIssues, agentLogs } = useSSE()
  const [runtimeSeconds, setRuntimeSeconds] = useState(0)
  const startTime = state?.stats.StartTime

  useEffect(() => {
    if (!startTime) {
      setRuntimeSeconds(0)
      return
    }

    setRuntimeSeconds(computeRuntimeSeconds(startTime))

    const timer = window.setInterval(() => {
      setRuntimeSeconds(computeRuntimeSeconds(startTime))
    }, 1000)

    return () => window.clearInterval(timer)
  }, [startTime])

  if (!state) {
    return (
      <div className="dashboard">
        <Header connected={connected} runtimeSeconds={runtimeSeconds} />
        <section className="dashboard__notice" aria-live="polite">
          <p className="dashboard__notice-title">Connecting to orchestrator stream...</p>
        </section>
      </div>
    )
  }

  return (
    <div className="dashboard">
      <Header connected={connected} runtimeSeconds={runtimeSeconds} />

      {error ? (
        <section className="dashboard__notice dashboard__notice--error" role="alert">
          <p className="dashboard__notice-title">Connection error</p>
          <p className="dashboard__notice-message">{error}</p>
        </section>
      ) : null}

      <MetricCards
        stats={state.stats}
        backoffCount={state.backoff.length}
        runtimeSeconds={runtimeSeconds}
      />

      <main className="dashboard__content" aria-label="Orchestrator activity">
        <section className="dashboard__panel">
          <h2 className="dashboard__panel-title">Running Sessions</h2>
          <SessionsTable entries={state.running} />
        </section>

        <section className="dashboard__panel">
          <h2 className="dashboard__panel-title">Retry Queue</h2>
          <RetryQueue entries={state.backoff} />
        </section>

        <section className="dashboard__panel">
          <h2 className="dashboard__panel-title">Rate Limits</h2>
          <RateLimits limits={[]} />
        </section>

        {teamSnapshot ? (
          <>
            <section className="dashboard__panel">
              <h2 className="dashboard__panel-title">Team Status</h2>
              <TeamTable snapshot={teamSnapshot} />
            </section>

            <section className="dashboard__panel">
              <h2 className="dashboard__panel-title">Workers</h2>
              <WorkerTable workers={teamSnapshot.workers} />
            </section>
          </>
        ) : null}

        <section className="dashboard__panel">
          <h2 className="dashboard__panel-title">Board</h2>
          <BoardView issues={boardIssues} />
        </section>

        {agentLogs.length > 0 ? (
          <section className="dashboard__panel">
            <h2 className="dashboard__panel-title">Agent Logs</h2>
            <AgentLogs logs={agentLogs} />
          </section>
        ) : null}
      </main>
    </div>
  )
}

export default App
