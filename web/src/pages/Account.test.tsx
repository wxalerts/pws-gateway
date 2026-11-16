import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type * as ApiModule from '../api';
import type { Claim } from '../api';
import { spokenCode } from '../lib/claim';
import { qrPath } from '../lib/qr';
import { AccountPage, PendingClaim } from './Account';

const get = vi.fn<() => Promise<Claim>>();
const start = vi.fn<() => Promise<Claim>>();
const unlink = vi.fn<() => Promise<Claim>>();

vi.mock('../api', async () => {
  const real = await vi.importActual<typeof ApiModule>('../api');
  return {
    ...real,
    api: {
      ...real.api,
      claim: {
        get: () => get(),
        start: () => start(),
        cancel: vi.fn(),
        unlink: () => unlink(),
      },
      uplink: () =>
        Promise.resolve({
          connected: true,
          broker: 'wss://pws-mqtt.wxalerts.org/mqtt',
          device_id: 'd_abcdefghij',
          last_publish: null,
          outbox: { depth: 0, oldest_s: 0 },
        }),
    },
  };
});

function claim(over: Partial<Claim>): Claim {
  return {
    state: 'unclaimed',
    user_code: null,
    verification_uri: null,
    verification_uri_complete: null,
    expires_at: null,
    device_id: null,
    error: null,
    ...over,
  };
}

const pending = claim({
  state: 'pending',
  user_code: 'K7MP-2XQD',
  verification_uri: 'https://wxalerts.org/pws/claim',
  verification_uri_complete: 'https://wxalerts.org/pws/claim?code=K7MP2XQD',
  expires_at: new Date(Date.now() + 9 * 60_000 + 30_000).toISOString(),
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('qrPath', () => {
  it('draws a square grid of modules for a short address', () => {
    const { path, modules } = qrPath('https://wxalerts.org/pws/claim?code=K7MP2XQD');
    expect(modules).toBeGreaterThanOrEqual(21);
    expect((modules - 17) % 4).toBe(0);
    expect(path.startsWith('M')).toBe(true);
  });
});

describe('PendingClaim', () => {
  it('shows the code, the link, a countdown and a QR code', () => {
    render(
      <PendingClaim
        claim={pending}
        busy={false}
        onStartOver={vi.fn()}
        onCancel={vi.fn()}
        onExpired={vi.fn()}
      />,
    );
    expect(screen.getByLabelText(`Code: ${spokenCode('K7MP-2XQD')}`)).toHaveTextContent(
      'K7MP-2XQD',
    );
    const link = screen.getByRole('link', { name: 'https://wxalerts.org/pws/claim' });
    expect(link).toHaveAttribute('href', 'https://wxalerts.org/pws/claim');
    expect(screen.getByRole('timer')).toHaveTextContent(/Code expires in 9:(2|3)\d/);
    expect(screen.getByRole('img', { name: /QR code/ })).toBeInTheDocument();
  });

  it('offers Start over and Cancel', async () => {
    const onStartOver = vi.fn();
    const onCancel = vi.fn();
    render(
      <PendingClaim
        claim={pending}
        busy={false}
        onStartOver={onStartOver}
        onCancel={onCancel}
        onExpired={vi.fn()}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Start over' }));
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onStartOver).toHaveBeenCalledOnce();
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it('reports expiry', () => {
    const onExpired = vi.fn();
    const gone = { ...pending, expires_at: new Date(Date.now() - 1000).toISOString() };
    render(
      <PendingClaim
        claim={gone}
        busy={false}
        onStartOver={vi.fn()}
        onCancel={vi.fn()}
        onExpired={onExpired}
      />,
    );
    expect(screen.getByRole('timer')).toHaveTextContent('Code expired');
    expect(onExpired).toHaveBeenCalled();
  });
});

describe('AccountPage', () => {
  function renderPage() {
    return render(
      <MemoryRouter>
        <AccountPage />
      </MemoryRouter>,
    );
  }

  it('starts a claim from the unlinked state and explains why a code ended', async () => {
    get.mockResolvedValue(claim({ error: 'expired' }));
    start.mockResolvedValue(pending);
    renderPage();
    expect(await screen.findByText(/ran out of time/)).toBeInTheDocument();
    get.mockResolvedValue(pending);
    await userEvent.click(screen.getByRole('button', { name: 'Get a code' }));
    expect(start).toHaveBeenCalledOnce();
    expect(await screen.findByLabelText(/Code: K 7 M P 2 X Q D/)).toBeInTheDocument();
  });

  it('shows the device id, the dashboard link and the connection once linked', async () => {
    get.mockResolvedValue(claim({ state: 'claimed', device_id: 'd_abcdefghij' }));
    renderPage();
    expect(await screen.findByText('d_abcdefghij')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Open your WxAlerts dashboard/ })).toHaveAttribute(
      'href',
      'https://wxalerts.org/pws',
    );
    expect(await screen.findByText('Connected')).toBeInTheDocument();
  });

  it('asks before unlinking', async () => {
    get.mockResolvedValue(claim({ state: 'claimed', device_id: 'd_abcdefghij' }));
    unlink.mockResolvedValue(claim({}));
    renderPage();
    await userEvent.click(await screen.findByRole('button', { name: 'Unlink this gateway' }));
    expect(unlink).not.toHaveBeenCalled();
    get.mockResolvedValue(claim({}));
    await userEvent.click(screen.getByRole('button', { name: 'Yes, unlink this gateway' }));
    await waitFor(() => expect(unlink).toHaveBeenCalledOnce());
  });

  it('shows a plain message when starting fails', async () => {
    get.mockResolvedValue(claim({}));
    start.mockRejectedValue(new Error('Could not reach WxAlerts.'));
    renderPage();
    await userEvent.click(await screen.findByRole('button', { name: 'Get a code' }));
    expect(await screen.findByText('Could not reach WxAlerts.')).toBeInTheDocument();
  });
});
