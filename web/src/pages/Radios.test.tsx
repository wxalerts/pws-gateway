import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Radio } from '../api';
import { RadioRoleForm } from './Radios';

const dongle: Radio = {
  serial: '00000001',
  bus: '1-1.2',
  product: 'RTL2838UHIDIR',
  role: 'disabled',
  band_mhz: 433.92,
  status: 'idle',
};

const other: Radio = {
  serial: '00000002',
  bus: '1-1.3',
  product: 'RTL2838UHIDIR',
  role: 'rtl_433',
  band_mhz: 915,
  status: 'running',
};

describe('RadioRoleForm', () => {
  it('shows serial, product and status, and explains that one dongle tunes one band', () => {
    render(<RadioRoleForm radio={dongle} others={[]} onSave={vi.fn()} />);
    expect(screen.getByRole('heading', { name: 'RTL2838UHIDIR' })).toBeInTheDocument();
    expect(screen.getByText('00000001')).toBeInTheDocument();
    expect(screen.getByText('idle')).toBeInTheDocument();
    expect(screen.getByText(/A dongle listens to one band at a time/)).toBeInTheDocument();
  });

  it('keeps Save disabled until something changes', () => {
    render(<RadioRoleForm radio={dongle} others={[]} onSave={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  });

  it('submits the chosen role and band', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<RadioRoleForm radio={dongle} others={[]} onSave={onSave} />);

    await user.click(screen.getByRole('radio', { name: /Receive weather stations/ }));
    await user.selectOptions(screen.getByRole('combobox', { name: 'Band' }), '915');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(onSave).toHaveBeenCalledWith({ role: 'rtl_433', band_mhz: 915 });
    expect(await screen.findByText(/Saved\./)).toBeInTheDocument();
  });

  it('warns when another dongle already covers the same band', async () => {
    const user = userEvent.setup();
    render(<RadioRoleForm radio={dongle} others={[other]} onSave={vi.fn()} />);

    expect(screen.queryByText(/Another dongle already listens/)).toBeNull();
    await user.click(screen.getByRole('radio', { name: /Receive weather stations/ }));
    await user.selectOptions(screen.getByRole('combobox', { name: 'Band' }), '915');
    expect(screen.getByText(/Another dongle already listens on 915 MHz/)).toBeInTheDocument();
  });

  it('disables the band select when the dongle is disabled', () => {
    render(<RadioRoleForm radio={dongle} others={[]} onSave={vi.fn()} />);
    expect(screen.getByRole('combobox', { name: 'Band' })).toBeDisabled();
  });

  it('shows the server error when saving fails', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockRejectedValue(new Error('band_mhz must be 433.92 or 915'));
    render(<RadioRoleForm radio={dongle} others={[]} onSave={onSave} />);
    await user.click(screen.getByRole('radio', { name: /Receive weather stations/ }));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('band_mhz must be 433.92 or 915');
  });
});
