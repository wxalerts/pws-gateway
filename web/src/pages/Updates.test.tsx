import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type * as ApiModule from '../api';
import type { UpdateStatus } from '../api';
import { UpdatesPage, validateWindow } from './Updates';

const get = vi.fn<() => Promise<UpdateStatus>>();
const check = vi.fn();
const install = vi.fn();
const saveSettings = vi.fn();

vi.mock('../api', async () => {
  const real = await vi.importActual<typeof ApiModule>('../api');
  return {
    ...real,
    api: {
      ...real.api,
      update: () => get(),
      updates: {
        check: () => check(),
        install: () => install(),
        saveSettings: (s: unknown) => saveSettings(s),
      },
      version: () =>
        Promise.resolve({ version: '0.3.1', commit: 'abc', build_date: '2026-11-10T00:00:00Z' }),
    },
  };
});

function upd(over: Partial<UpdateStatus> = {}): UpdateStatus {
  return {
    current: '0.3.1',
    target: null,
    state: 'idle',
    error: null,
    ts: '2026-11-17T00:00:00Z',
    window: '02:00-04:00',
    channel: 'stable',
    enabled: true,
    ...over,
  };
}

afterEach(() => vi.clearAllMocks());

describe('validateWindow', () => {
  it('accepts HH:MM-HH:MM and rejects the rest', () => {
    expect(validateWindow('02:00-04:00')).toBeNull();
    expect(validateWindow('22:30-01:15')).toBeNull();
    expect(validateWindow('2:00-4:00')).not.toBeNull();
    expect(validateWindow('25:00-04:00')).not.toBeNull();
    expect(validateWindow('02:00-02:00')).not.toBeNull();
    expect(validateWindow('')).not.toBeNull();
  });
});

describe('UpdatesPage', () => {
  it('explains a window deferral and shows the os note and last check', async () => {
    get.mockResolvedValue(
      upd({
        state: 'deferred',
        target: '0.3.2',
        defer_kind: 'window',
        detail: 'Opens at 02:00.',
        os_deferred: true,
        last_check: '2026-11-17T00:00:00Z',
      }),
    );
    render(<UpdatesPage />);
    expect(await screen.findByText(/Waiting for the update window/)).toBeInTheDocument();
    expect(screen.getByText('Opens at 02:00.')).toBeInTheDocument();
    expect(screen.getByText(/full system image is waiting/)).toBeInTheDocument();
    expect(screen.getByText('Last check')).toBeInTheDocument();
  });

  it('shows a progress bar while downloading and disables the buttons', async () => {
    get.mockResolvedValue(upd({ state: 'downloading', progress: 40 }));
    render(<UpdatesPage />);
    const bar = await screen.findByRole('progressbar');
    expect(bar).toHaveAttribute('value', '40');
    expect(screen.getByRole('button', { name: 'Check now' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Install now' })).toBeDisabled();
  });

  it('shows the failure code and text', async () => {
    get.mockResolvedValue(upd({ state: 'failed', code: 'bad_signature', error: 'sig mismatch' }));
    render(<UpdatesPage />);
    expect(await screen.findByText('bad_signature')).toBeInTheDocument();
    expect(screen.getByText('sig mismatch')).toBeInTheDocument();
  });

  it('shows a reconnecting hint while rebooting', async () => {
    get.mockResolvedValue(upd({ state: 'rebooting' }));
    render(<UpdatesPage />);
    expect(await screen.findByText(/Reconnecting/)).toBeInTheDocument();
  });

  it('says updates are off on a development build', async () => {
    get.mockResolvedValue(upd({ enabled: false }));
    render(<UpdatesPage />);
    expect(
      await screen.findByText('This is a development build, updates are off.'),
    ).toBeInTheDocument();
  });

  it('Check now calls the check endpoint', async () => {
    const user = userEvent.setup();
    check.mockResolvedValue({ started: true });
    get.mockResolvedValue(upd());
    render(<UpdatesPage />);
    await user.click(await screen.findByRole('button', { name: 'Check now' }));
    await waitFor(() => expect(check).toHaveBeenCalledTimes(1));
  });

  it('Install now asks first, then calls apply', async () => {
    const user = userEvent.setup();
    install.mockResolvedValue({ started: true });
    get.mockResolvedValue(upd());
    render(<UpdatesPage />);
    await user.click(await screen.findByRole('button', { name: 'Install now' }));
    expect(install).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Yes, install and restart' }));
    await waitFor(() => expect(install).toHaveBeenCalledTimes(1));
  });

  it('blocks a bad window and saves a good one', async () => {
    const user = userEvent.setup();
    saveSettings.mockResolvedValue({});
    get.mockResolvedValue(upd());
    render(<UpdatesPage />);
    const field = await screen.findByRole('textbox', { name: 'Update window' });
    await user.clear(field);
    await user.type(field, '9-10');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/Use the form 02:00-04:00/)).toBeInTheDocument();
    expect(saveSettings).not.toHaveBeenCalled();
    await user.clear(field);
    await user.type(field, '01:00-03:00');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(saveSettings).toHaveBeenCalledWith({ window: '01:00-03:00', channel: 'stable' }),
    );
  });
});
