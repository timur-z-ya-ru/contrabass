export function DependencyGraph() {
  return (
    <div style={{ padding: '1rem', border: '1px solid #333', borderRadius: '8px', margin: '1rem 0' }}>
      <h3 style={{ margin: '0 0 0.5rem' }}>Dependency Graph</h3>
      <svg width="400" height="200" viewBox="0 0 400 200">
        <text x="200" y="100" textAnchor="middle" fill="#666" fontSize="14">
          DAG visualization — connect to /api/v1/wave/status for live data
        </text>
      </svg>
    </div>
  );
}
