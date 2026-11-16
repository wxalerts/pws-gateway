import { useEffect, useRef, useState } from 'react';
import { api, errorMessage } from '../api';
import { Alert } from '../components/Alert';
import { Button } from '../components/Button';
import { Card } from '../components/Card';
import { CheckboxField, SelectField } from '../components/fields';
import { formatRelative } from '../lib/format';
import { usePoll } from '../lib/usePoll';

const LINE_OPTIONS = [100, 200, 500, 1000];

export function LogsPage() {
  const [lines, setLines] = useState(200);
  const [follow, setFollow] = useState(true);
  const poll = usePoll((s) => api.logs(lines, { signal: s }), follow ? 3000 : 3_600_000, [
    lines,
    follow,
  ]);
  const preRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    document.title = 'Logs | PWS Gateway';
  }, []);

  // Keep the view pinned to the newest line while following.
  useEffect(() => {
    if (follow && preRef.current) preRef.current.scrollTop = preRef.current.scrollHeight;
  }, [poll.data, follow]);

  function download() {
    const blob = new Blob([poll.data ?? ''], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `pwsgw-${new Date().toISOString().replace(/[:.]/g, '-')}.log`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <>
      <div className="page-header">
        <h1>Logs</h1>
        <span className="muted" role="status" aria-live="polite">
          {poll.updatedAt
            ? `Updated ${formatRelative(new Date(poll.updatedAt).toISOString())}`
            : 'Loading...'}
        </span>
      </div>
      <div className="stack">
        <p className="muted">
          The most recent lines from the gateway service. When asking for help, use Download and
          attach the file.
        </p>
        <Card
          id="logs"
          actions={
            <div className="btn-row">
              <SelectField<number>
                label={<span className="visually-hidden">Lines to show</span>}
                value={lines}
                onChange={setLines}
                options={LINE_OPTIONS.map((n) => ({ value: n, label: `${n} lines` }))}
                className="input input-small"
              />
              <CheckboxField
                label="Follow"
                checked={follow}
                onChange={(e) => setFollow(e.target.checked)}
              />
              <Button onClick={() => void poll.refresh()}>Refresh</Button>
              <Button onClick={download} disabled={!poll.data}>
                Download
              </Button>
            </div>
          }
        >
          {poll.error && !poll.data && <Alert tone="bad">{errorMessage(poll.error)}</Alert>}
          <pre ref={preRef} className="log-view" aria-label="Gateway log output">
            {poll.data ?? ''}
          </pre>
        </Card>
      </div>
    </>
  );
}
