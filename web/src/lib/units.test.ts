import { describe, expect, it } from 'vitest';
import {
  compassPoint,
  convert,
  formatField,
  readUnitSystem,
  UNIT_STORAGE_KEY,
  unitLabel,
  writeUnitSystem,
} from './units';

describe('convert', () => {
  it('leaves SI values alone for metric except wind speed', () => {
    expect(convert(21.5, 'temperature', 'metric')).toBe(21.5);
    expect(convert(1013.2, 'pressure', 'metric')).toBe(1013.2);
    expect(convert(12.7, 'rain', 'metric')).toBe(12.7);
    expect(convert(10, 'speed', 'metric')).toBeCloseTo(36, 6); // m/s to km/h
  });

  it('converts to US customary', () => {
    expect(convert(0, 'temperature', 'us')).toBe(32);
    expect(convert(100, 'temperature', 'us')).toBe(212);
    expect(convert(1, 'speed', 'us')).toBeCloseTo(2.23694, 4);
    expect(convert(1013.25, 'pressure', 'us')).toBeCloseTo(29.92, 2);
    expect(convert(25.4, 'rain', 'us')).toBeCloseTo(1, 9);
    expect(convert(10, 'distance', 'us')).toBeCloseTo(6.21371, 4);
    expect(convert(1, 'length', 'us')).toBeCloseTo(3.28084, 4);
  });

  it('does not convert unitless quantities', () => {
    expect(convert(55, 'percent', 'us')).toBe(55);
    expect(convert(7.2, 'index', 'us')).toBe(7.2);
    expect(convert(270, 'direction', 'us')).toBe(270);
  });
});

describe('unitLabel', () => {
  it('names units per system', () => {
    expect(unitLabel('temperature', 'metric')).toBe('°C');
    expect(unitLabel('temperature', 'us')).toBe('°F');
    expect(unitLabel('speed', 'metric')).toBe('km/h');
    expect(unitLabel('speed', 'us')).toBe('mph');
    expect(unitLabel('pressure', 'us')).toBe('inHg');
    expect(unitLabel('percent', 'us')).toBe('%');
  });
});

describe('formatField', () => {
  it('formats SI fields for metric display', () => {
    expect(formatField('temp_c', 21.46, 'metric')).toBe('21.5 °C');
    expect(formatField('humidity_pct', 64.4, 'metric')).toBe('64 %');
    expect(formatField('wind_speed_ms', 5, 'metric')).toBe('18.0 km/h');
    expect(formatField('rain_mm', 3.25, 'metric')).toBe('3.3 mm');
  });

  it('formats SI fields for US display with sensible precision', () => {
    expect(formatField('temp_c', 20, 'us')).toBe('68 °F');
    expect(formatField('pressure_station_hpa', 1013.25, 'us')).toBe('29.92 inHg');
    expect(formatField('rain_mm', 25.4, 'us')).toBe('1.00 in');
    expect(formatField('wind_gust_ms', 10, 'us')).toBe('22.4 mph');
  });

  it('renders direction with a compass point and battery as text', () => {
    expect(formatField('wind_dir_deg', 90, 'metric')).toBe('90° E');
    expect(formatField('wind_dir_deg', 359, 'us')).toBe('359° N');
    expect(formatField('battery_ok', 1, 'metric')).toBe('OK');
    expect(formatField('battery_ok', 0, 'metric')).toBe('Low');
  });

  it('falls back gracefully for unknown fields', () => {
    expect(formatField('mystery_thing', 1.2345, 'us')).toBe('1.23');
  });
});

describe('compassPoint', () => {
  it('maps degrees to sixteen points', () => {
    expect(compassPoint(0)).toBe('N');
    expect(compassPoint(22.5)).toBe('NNE');
    expect(compassPoint(180)).toBe('S');
    expect(compassPoint(292.5)).toBe('WNW');
    expect(compassPoint(-90)).toBe('W');
  });
});

describe('unit preference storage', () => {
  it('defaults to metric and round-trips through storage', () => {
    const store = new Map<string, string>();
    const fake = {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
    };
    expect(readUnitSystem(fake)).toBe('metric');
    writeUnitSystem(fake, 'us');
    expect(store.get(UNIT_STORAGE_KEY)).toBe('us');
    expect(readUnitSystem(fake)).toBe('us');
    expect(readUnitSystem(null)).toBe('metric');
  });

  it('ignores garbage and storage errors', () => {
    expect(readUnitSystem({ getItem: () => 'banana' })).toBe('metric');
    expect(
      readUnitSystem({
        getItem: () => {
          throw new Error('blocked');
        },
      }),
    ).toBe('metric');
    expect(() =>
      writeUnitSystem(
        {
          setItem: () => {
            throw new Error('blocked');
          },
        },
        'us',
      ),
    ).not.toThrow();
  });
});
