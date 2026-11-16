import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Station } from '../api';
import { DataToUse } from './DataToUse';

const base: Station = {
  key: 'Acurite-609TXC:224:',
  model: 'Acurite-609TXC',
  id: 224,
  channel: null,
  last_seen: new Date().toISOString(),
  rssi_db: -60,
  snr_db: 10,
  battery_ok: true,
  fields: ['temp_c', 'humidity_pct'],
  fields_available: ['temp_c', 'humidity_pct', 'battery_ok'],
  raw_fields: ['status'],
  fields_default: ['temp_c', 'humidity_pct'],
  include_raw: true,
  n_sightings: 10,
  selected: true,
  name: '',
  alt_m: null,
  wind_height_m: null,
};

const latest = { temp_c: { value: 14.3, ts: '2026-11-16T17:00:00Z' } };

describe('DataToUse', () => {
  it('ticks the defaults and shows labels and latest values', () => {
    render(<DataToUse station={base} latest={latest} system="metric" onSave={vi.fn()} />);
    expect(screen.getByRole('checkbox', { name: /Temperature/ })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: /Humidity/ })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: /Battery/ })).not.toBeChecked();
    expect(screen.getByText('14.3 °C')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset to defaults' })).toBeDisabled();
  });

  it('saves the checklist and the raw toggle, and resets to defaults', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<DataToUse station={base} latest={latest} system="metric" onSave={onSave} />);

    await user.click(screen.getByRole('checkbox', { name: /Battery/ }));
    await user.click(screen.getByRole('checkbox', { name: /Humidity/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Include other decoder fields' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).toHaveBeenLastCalledWith({
      fields: { temp_c: true, humidity_pct: false, battery_ok: true },
      include_raw: false,
    });

    await user.click(screen.getByRole('button', { name: 'Reset to defaults' }));
    expect(screen.getByRole('checkbox', { name: /Humidity/ })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: /Battery/ })).not.toBeChecked();
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).toHaveBeenLastCalledWith({ fields: null, include_raw: false });
  });
});
