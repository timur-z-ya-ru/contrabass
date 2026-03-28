import { useEffect, useState } from 'react';

interface WaveEvent {
  ts: string;
  type: string;
  issue_id?: string;
  issues?: string[];
}

export function WaveTimeline() {
  const [events, setEvents] = useState<WaveEvent[]>([]);

  useEffect(() => {
    fetch('/api/v1/wave/events')
      .then(r => r.json())
      .then(setEvents)
      .catch(console.error);
  }, []);

  return (
    <div style={{ padding: '1rem', border: '1px solid #333', borderRadius: '8px', margin: '1rem 0' }}>
      <h3 style={{ margin: '0 0 0.5rem' }}>Wave Timeline</h3>
      {events.length === 0 ? (
        <div style={{ color: '#666' }}>No wave events yet</div>
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {events.map((e, i) => (
            <li key={i} style={{ padding: '0.25rem 0', borderBottom: '1px solid #222' }}>
              <span style={{ color: '#888' }}>{e.ts}</span> — {e.type}
              {e.issue_id && <span> (#{e.issue_id})</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
