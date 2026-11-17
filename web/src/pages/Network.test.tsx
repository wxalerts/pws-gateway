import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type * as ApiModule from '../api';
import { ApiError, type NetworkStatus } from '../api';
import { NetworkPage } from './Network';

const get = vi.fn<() => Promise<NetworkStatus>>();
const apply = vi.fn();
const confirm = vi.fn();
const revert = vi.fn();
const wifi = vi.fn();
const remove = vi.fn();

vi.mock('../api', async () => {
  const real = await vi.importActual<typeof ApiModule>('../api');
  return {
    ...real,
    api: {
      ...real.api,
      network: {
        get: () => get(),
        apply: (r: unknown) => apply(r),
        confirm: () => confirm(),
        revert: () => revert(),
        wifi: () => wifi(),
        remove: (n: string) => remove(n),
      },
    },
  };
});

function status(over: Partial<NetworkStatus> = {}): NetworkStatus {
  return {
    devices: [
      {
        name: 'eth0',
        type: 'ethernet',
        state: 'connected',
        active: 'Wired',
        addresses: ['192.168.1.42/24'],
      },
      { name: 'wlan0', type: 'wifi', state: 'connected', active: 'HomeWiFi' },
    ],
    connections: [
      { name: 'Wired', type: 'ethernet', autoconnect: true, ipv4: { method: 'auto' } },
      {
        name: 'HomeWiFi',
        type: 'wifi',
        autoconnect: true,
        ipv4: { method: 'auto' },
        wifi: { ssid: 'HomeWiFi' },
      },
      {
        name: 'Garage',
        type: 'wifi',
        autoconnect: true,
        ipv4: { method: 'auto' },
        wifi: { ssid: 'Garage' },
      },
    ],
    connectivity: 'full',
    hostname: 'pwsgw',
    ...over,
  };
}

beforeEach(() => {
  get.mockResolvedValue(status());
});
afterEach(() => vi.clearAllMocks());

describe('NetworkPage', () => {
  it('lists devices, connectivity and disables deleting the active Wi-Fi', async () => {
    render(<NetworkPage />);
    expect(await screen.findByText('192.168.1.42/24')).toBeInTheDocument();
    expect(screen.getByText('Online')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete HomeWiFi' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Delete Garage' })).toBeEnabled();
  });

  it('shows the hotspot notice with the SSID', async () => {
    get.mockResolvedValue(status({ hotspot: { ssid: 'pwsgw-setup' } }));
    render(<NetworkPage />);
    expect(
      await screen.findByText(/Setup hotspot is on because no known network could be reached/),
    ).toBeInTheDocument();
    expect(screen.getByText('pwsgw-setup')).toBeInTheDocument();
  });

  it('sends only the changed profile, then shows the banner with a countdown', async () => {
    const user = userEvent.setup();
    apply.mockResolvedValue({
      applied: true,
      confirm_deadline: new Date(Date.now() + 90_000).toISOString(),
      changed: ['Wired'],
    });
    render(<NetworkPage />);
    const dns = await screen.findByRole('textbox', { name: 'DNS servers' });
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled();
    await user.type(dns, '1.1.1.1');
    await user.click(screen.getByRole('button', { name: 'Apply' }));

    await waitFor(() => expect(apply).toHaveBeenCalledTimes(1));
    const sent = apply.mock.calls[0][0] as { connections: { name: string }[]; hostname?: string };
    expect(sent.connections.map((c) => c.name)).toEqual(['Wired']);
    expect(sent.hostname).toBeUndefined();
    expect(await screen.findByText('Confirm the new network settings')).toBeInTheDocument();
    expect(screen.getByText(/1:30|1:29/)).toBeInTheDocument();
  });

  it('warns before applying a change to the address in use', async () => {
    const user = userEvent.setup();
    apply.mockResolvedValue({
      applied: true,
      confirm_deadline: new Date(Date.now() + 90_000).toISOString(),
      changed: ['Wired'],
    });
    render(<NetworkPage />);
    await screen.findByText('Online');
    await user.click(screen.getByRole('radio', { name: /Fixed address/ }));
    await user.type(screen.getByRole('textbox', { name: /Address with prefix/ }), '10.0.0.5/24');
    await user.click(screen.getByRole('button', { name: 'Apply' }));
    expect(apply).not.toHaveBeenCalled();
    expect(screen.getByText('This changes the address you are using')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Apply anyway' }));
    await waitFor(() => expect(apply).toHaveBeenCalledTimes(1));
  });

  it('Confirm and Undo call their endpoints', async () => {
    const user = userEvent.setup();
    get.mockResolvedValue(
      status({
        pending: { deadline: new Date(Date.now() + 60_000).toISOString(), changed: ['Wired'] },
      }),
    );
    confirm.mockResolvedValue({ confirmed: true });
    revert.mockResolvedValue({ reverted: true });
    render(<NetworkPage />);
    await user.click(await screen.findByRole('button', { name: 'Confirm and keep' }));
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
    await user.click(await screen.findByRole('button', { name: 'Undo now' }));
    await waitFor(() => expect(revert).toHaveBeenCalledTimes(1));
  });

  it('shows the 422 detail', async () => {
    const user = userEvent.setup();
    apply.mockRejectedValue(
      new ApiError(422, { error: 'invalid_network', detail: 'DNS server 999.1.1.1 is not valid' }),
    );
    render(<NetworkPage />);
    await user.type(await screen.findByRole('textbox', { name: 'DNS servers' }), '999.1.1.1');
    await user.click(screen.getByRole('button', { name: 'Apply' }));
    expect(await screen.findByText('DNS server 999.1.1.1 is not valid')).toBeInTheDocument();
  });

  it('scans for Wi-Fi and saves a new profile with its passphrase', async () => {
    const user = userEvent.setup();
    wifi.mockResolvedValue([{ ssid: 'Cafe', signal: 60, secured: true }]);
    apply.mockResolvedValue({
      applied: true,
      confirm_deadline: new Date(Date.now() + 90_000).toISOString(),
      changed: ['Cafe'],
    });
    render(<NetworkPage />);
    await user.click(await screen.findByRole('button', { name: 'Scan for networks' }));
    expect(await screen.findByText(/Found 1 network/)).toBeInTheDocument();
    await user.type(screen.getByLabelText(/Network name/), 'Cafe');
    await user.type(screen.getByLabelText('Wi-Fi password'), 'hunter22');
    await user.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(apply).toHaveBeenCalledTimes(1));
    const sent = apply.mock.calls[0][0] as {
      connections: { name: string; wifi?: { psk?: string } }[];
    };
    expect(sent.connections).toHaveLength(1);
    expect(sent.connections[0].name).toBe('Cafe');
    expect(sent.connections[0].wifi?.psk).toBe('hunter22');
  });
});
