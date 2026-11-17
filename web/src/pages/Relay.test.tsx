import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { CwopConfig, RelayStatus } from '../api';
import { RelayDelivery, RelayForm } from './Relay';

const config: CwopConfig = {
  owner: '',
  station: '',
  id: '',
  server: '',
  position_opt_in: false,
  lat: 0,
  lon: 0,
};

const health = { last_attempt: null, last_success: null, last_error: null, failures: 0 };

function data(over: Partial<RelayStatus['cwop']['status']>): RelayStatus['cwop'] {
  return {
    config,
    needs_passcode: false,
    status: {
      state: 'idle',
      problems: [],
      passcode_set: false,
      health,
      next_attempt: null,
      ...over,
    },
  };
}

describe('RelayDelivery', () => {
  it('shows never and none before anything is sent', () => {
    render(<RelayDelivery data={data({})} />);
    expect(screen.getByText('Never')).toBeInTheDocument();
    expect(screen.getByText('None')).toBeInTheDocument();
  });

  it('shows the last error code and message', () => {
    render(
      <RelayDelivery
        data={data({
          state: 'error',
          health: {
            ...health,
            failures: 2,
            last_error: {
              code: 'connect',
              message: 'dial tcp: refused',
              at: new Date().toISOString(),
            },
          },
        })}
      />,
    );
    expect(screen.getByText('connect')).toBeInTheDocument();
    expect(screen.getByText(/dial tcp: refused/)).toBeInTheDocument();
    expect(screen.getByText('Last send failed')).toBeInTheDocument();
  });

  it('says WxAlerts relays a server-owned station', () => {
    render(<RelayDelivery data={data({ state: 'server' })} />);
    expect(screen.getByText(/The Pi sends nothing/)).toBeInTheDocument();
  });
});

describe('RelayForm', () => {
  it('hides the credentials until the Pi is chosen as sender, and saves the passcode value only via onSave', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <RelayForm
        config={config}
        stations={[{ key: 'Acurite-5n1:1234:A', name: '' }]}
        needsPasscode={false}
        passcodeSet={false}
        onSave={onSave}
      />,
    );
    expect(screen.queryByLabelText('CWOP ID')).not.toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('Who sends to CWOP'), 'device');
    await userEvent.selectOptions(screen.getByLabelText('Station'), 'Acurite-5n1:1234:A');
    await userEvent.type(screen.getByLabelText('CWOP ID'), 'cw1234');
    expect(screen.queryByLabelText('Validation code')).not.toBeInTheDocument();
    await userEvent.click(screen.getByLabelText(/Publish my station position/));
    await userEvent.type(screen.getByLabelText('Latitude'), '39.1');
    await userEvent.type(screen.getByLabelText('Longitude'), '-94.5');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        owner: 'device',
        id: 'CW1234',
        position_opt_in: true,
        lat: 39.1,
        lon: -94.5,
      }),
      '',
    );
  });

  it('asks for the validation code for a callsign', async () => {
    render(
      <RelayForm
        config={{ ...config, owner: 'device', id: 'W5XYZ' }}
        stations={[]}
        needsPasscode
        passcodeSet
        onSave={vi.fn()}
      />,
    );
    const code = screen.getByLabelText('Validation code') as HTMLInputElement;
    expect(code.type).toBe('password');
    expect(code.value).toBe('');
  });
});
