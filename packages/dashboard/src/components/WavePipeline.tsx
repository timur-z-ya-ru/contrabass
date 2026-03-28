import { useEffect, useState } from 'react';

interface WaveStatus {
  status: string;
  message: string;
}

export function WavePipeline() {
  const [data, setData] = useState<WaveStatus | null>(null);

  useEffect(() => {
    fetch('/api/v1/wave/status')
      .then(r => r.json())
      .then(setData)
      .catch(console.error);
  }, []);

  if (!data) return <div>Loading wave status...</div>;

  return (
    <div style={{ padding: '1rem', border: '1px solid #333', borderRadius: '8px', margin: '1rem 0' }}>
      <h3 style={{ margin: '0 0 0.5rem' }}>Wave Pipeline</h3>
      <div>Status: {data.status}</div>
      <div>{data.message}</div>
    </div>
  );
}
