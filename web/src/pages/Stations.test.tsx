import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { Station } from '../api';
import { StationsTable } from './Stations';

function station(overrides: Partial<Station>): Station {
  return {
    key: 'Fineoffset-WH65B:0x31',
    model: 'Fineoffset-WH65B',
    id: 49,
    channel: null,
    last_seen: new Date(Date.now() - 20_000).toISOString(),
    rssi_db: -62.1,
    snr_db: 21.4,
    battery_ok: true,
    fields: ['temp_c', 'humidity_pct', 'wind_speed_ms'],
    fields_available: ['temp_c', 'humidity_pct', 'wind_speed_ms', 'battery_ok'],
    raw_fields: [],
    fields_default: ['temp_c', 'humidity_pct', 'wind_speed_ms'],
    include_raw: false,
    n_sightings: 4812,
    selected: false,
    name: '',
    alt_m: null,
    wind_height_m: null,
    ...overrides,
  };
}

function renderTable(stations: Station[], onSelect = vi.fn(), onEdit = vi.fn()) {
  render(
    <MemoryRouter>
      <StationsTable stations={stations} onSelect={onSelect} onEdit={onEdit} />
    </MemoryRouter>,
  );
  return { onSelect, onEdit };
}

describe('StationsTable', () => {
  it('shows an empty message when nothing has been heard', () => {
    renderTable([]);
    expect(screen.getByText(/No stations heard yet/)).toBeInTheDocument();
  });

  it('lists model, id, channel, signal, battery and fields', () => {
    renderTable([
      station({}),
      station({
        key: 'Acurite-5n1:0x0C4A:A',
        model: 'Acurite-5n1',
        id: 3146,
        channel: 'A',
        rssi_db: -88.2,
        snr_db: 5.1,
        battery_ok: false,
        name: 'Front yard',
        fields: ['temp_c', 'rain_mm'],
      }),
    ]);

    const rows = screen.getAllByRole('row').slice(1); // skip header
    expect(rows).toHaveLength(2);

    const first = within(rows[0]);
    expect(first.getByRole('link', { name: 'Fineoffset-WH65B' })).toHaveAttribute(
      'href',
      '/stations/Fineoffset-WH65B%3A0x31',
    );
    expect(first.getByText('Fineoffset-WH65B id 49')).toBeInTheDocument();
    expect(first.getByText('-62 dB')).toBeInTheDocument();
    expect(first.getByText('OK')).toBeInTheDocument();
    expect(first.getByText('T H W')).toBeInTheDocument();
    expect(first.getByText('4,812')).toBeInTheDocument();

    const second = within(rows[1]);
    expect(second.getByRole('link', { name: 'Front yard' })).toBeInTheDocument();
    expect(second.getByText('Acurite-5n1 id 3146 ch A')).toBeInTheDocument();
    expect(second.getByText('Low')).toBeInTheDocument();
    expect(second.getByText('T R')).toBeInTheDocument();
  });

  it('shows unknown battery when the station reports no flag', () => {
    renderTable([station({ battery_ok: null })]);
    const row = within(screen.getAllByRole('row')[1]);
    expect(row.getByText('Unknown')).toBeInTheDocument();
    expect(row.queryByText('OK')).toBeNull();
    expect(row.queryByText('Low')).toBeNull();
  });

  it('marks the selected station and hides its Select button', () => {
    renderTable([
      station({ selected: true, name: 'Back garden' }),
      station({ key: 'other', id: 2 }),
    ]);
    const rows = screen.getAllByRole('row').slice(1);
    expect(rows[0]).toHaveClass('selected');
    expect(within(rows[0]).getByText('Selected for upload')).toBeInTheDocument();
    expect(within(rows[0]).queryByRole('button', { name: /^Select/ })).toBeNull();
    expect(within(rows[1]).getByRole('button', { name: /^Select/ })).toBeInTheDocument();
  });

  it('calls onSelect and onEdit with the right station', async () => {
    const user = userEvent.setup();
    const a = station({});
    const b = station({ key: 'Acurite-5n1:0x0C4A:A', model: 'Acurite-5n1', id: 3146 });
    const { onSelect, onEdit } = renderTable([a, b]);

    await user.click(screen.getByRole('button', { name: 'Select Acurite-5n1 for upload' }));
    expect(onSelect).toHaveBeenCalledWith(b);

    await user.click(screen.getByRole('button', { name: 'Edit Fineoffset-WH65B' }));
    expect(onEdit).toHaveBeenCalledWith(a);
  });

  it('flags stale stations', () => {
    renderTable([station({ last_seen: new Date(Date.now() - 2 * 86_400_000).toISOString() })]);
    const badge = screen.getByText('2d ago');
    expect(badge).toHaveClass('tone-bad');
  });
});
