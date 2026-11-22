import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Station } from '../api';
import { candidates, GroupSources } from './GroupSources';

function station(over: Partial<Station>): Station {
  return {
    key: 'Acurite-5n1:1:A',
    model: 'Acurite-5n1',
    id: 1,
    channel: 'A',
    last_seen: new Date().toISOString(),
    rssi_db: -60,
    snr_db: 10,
    battery_ok: true,
    fields: ['temp_c', 'humidity_pct', 'wind_speed_ms'],
    fields_available: [],
    raw_fields: [],
    fields_default: [],
    include_raw: false,
    n_sightings: 5,
    selected: true,
    name: 'Roof',
    alt_m: null,
    wind_height_m: null,
    ...over,
  };
}

const roof = station({});
const yard = station({
  key: 'Fineoffset-WH24:77:',
  model: 'Fineoffset-WH24',
  id: 77,
  channel: null,
  name: 'Back yard',
  fields: ['temp_c', 'rain_mm'],
});

describe('candidates', () => {
  it('lists only selected stations that report a field of the group', () => {
    expect(candidates([roof, yard], ['temp_c']).map((s) => s.key)).toEqual([roof.key, yard.key]);
    expect(candidates([roof, yard], ['rain_mm']).map((s) => s.key)).toEqual([yard.key]);
    expect(candidates([roof, { ...yard, selected: false }], ['rain_mm'])).toEqual([]);
  });
});

describe('GroupSources', () => {
  it('shows nothing when no group has two candidates', () => {
    const { container } = render(
      <GroupSources stations={[roof]} composite={{}} onSave={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('offers a labelled choice per group that someone reports, and warns when it is ambiguous', () => {
    render(<GroupSources stations={[roof, yard]} composite={{}} onSave={vi.fn()} />);
    const temp = screen.getByRole('combobox', { name: 'Temperature' });
    expect(temp).toHaveValue('');
    expect(screen.getByText(/2 stations report this/)).toBeInTheDocument();
    // Single-candidate groups are automatic and need no choice.
    expect(screen.getByRole('combobox', { name: 'Rain' })).toHaveDisplayValue(
      'Automatic (Back yard)',
    );
    // A group nobody reports is not listed.
    expect(screen.queryByRole('combobox', { name: 'Lightning' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Save choices' })).toBeDisabled();
  });

  it('saves the chosen station per group and drops unchosen ones', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<GroupSources stations={[roof, yard]} composite={{}} onSave={onSave} />);
    await user.selectOptions(screen.getByRole('combobox', { name: 'Temperature' }), yard.key);
    await user.click(screen.getByRole('button', { name: 'Save choices' }));
    expect(onSave).toHaveBeenCalledWith({ temperature: yard.key });
    expect(await screen.findByText('Saved.')).toBeInTheDocument();
  });

  it('keeps a saved choice and ignores one whose station is no longer a candidate', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <GroupSources
        stations={[roof, yard]}
        composite={{ temperature: roof.key, wind: 'gone:1:' }}
        onSave={onSave}
      />,
    );
    expect(screen.getByRole('combobox', { name: 'Temperature' })).toHaveValue(roof.key);
    await user.selectOptions(screen.getByRole('combobox', { name: 'Humidity' }), roof.key);
    await user.click(screen.getByRole('button', { name: 'Save choices' }));
    expect(onSave).toHaveBeenCalledWith({ temperature: roof.key, humidity: roof.key });
  });

  it('shows the server error and does not claim success', async () => {
    const user = userEvent.setup();
    render(
      <GroupSources
        stations={[roof, yard]}
        composite={{}}
        onSave={vi.fn().mockRejectedValue(new Error('config: bad key'))}
      />,
    );
    await user.selectOptions(screen.getByRole('combobox', { name: 'Temperature' }), roof.key);
    await user.click(screen.getByRole('button', { name: 'Save choices' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('config: bad key');
    expect(screen.queryByText('Saved.')).toBeNull();
  });
});
