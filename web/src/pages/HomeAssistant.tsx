import { useEffect } from 'react';
import { api, errorMessage } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Card } from '../components/Card';
import { usePoll } from '../lib/usePoll';

/** Placeholder. Shows whatever the gateway reports under config.homeassistant. */
export function HomeAssistantPage() {
  const config = usePoll((s) => api.config.get({ signal: s }), 30000);

  useEffect(() => {
    document.title = 'Home Assistant | PWS Gateway';
  }, []);

  const ha = (config.data?.homeassistant ?? null) as Record<string, unknown> | null;
  const enabled = ha?.enabled === true;

  return (
    <>
      <div className="page-header">
        <h1>Home Assistant</h1>
        {ha && <Badge tone={enabled ? 'ok' : 'neutral'}>{enabled ? 'Enabled' : 'Off'}</Badge>}
      </div>
      <div className="stack">
        <Card title="Local integration" id="ha">
          <p>
            The gateway will be able to publish your station's readings to Home Assistant on your
            own network, using MQTT discovery, so your sensors appear there with no cloud in
            between.
          </p>
          <p className="muted">
            This page is a placeholder. The settings form arrives in a later release; nothing is
            published to Home Assistant until then.
          </p>
          {config.error && !config.data && <Alert tone="bad">{errorMessage(config.error)}</Alert>}
          {ha && (
            <dl className="kv">
              {Object.entries(ha).map(([k, v]) => (
                <div key={k} className="kv-row">
                  <dt>{k.replace(/_/g, ' ')}</dt>
                  <dd className="mono">{v === null || v === undefined ? 'not set' : String(v)}</dd>
                </div>
              ))}
            </dl>
          )}
        </Card>
      </div>
    </>
  );
}
