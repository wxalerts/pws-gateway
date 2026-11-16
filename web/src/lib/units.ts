// Display-only unit conversion. The API and everything stored on the gateway are
// SI. A person picks "metric" or "US" once and the choice lives in localStorage.

export type UnitSystem = 'metric' | 'us';

export const UNIT_STORAGE_KEY = 'pwsgw.units';

export type Quantity =
  | 'temperature'
  | 'speed'
  | 'pressure'
  | 'rain'
  | 'distance'
  | 'length'
  | 'percent'
  | 'direction'
  | 'index'
  | 'lux'
  | 'irradiance'
  | 'count'
  | 'boolean';

export interface FieldMeta {
  /** Field name in the readings payload. */
  key: string;
  label: string;
  /** One to two letter label for compact lists. */
  short: string;
  quantity: Quantity;
  /** Decimal places when rendering a value. */
  digits: number;
}

export const FIELDS: FieldMeta[] = [
  { key: 'temp_c', label: 'Temperature', short: 'T', quantity: 'temperature', digits: 1 },
  { key: 'humidity_pct', label: 'Humidity', short: 'H', quantity: 'percent', digits: 0 },
  { key: 'pressure_station_hpa', label: 'Pressure', short: 'P', quantity: 'pressure', digits: 1 },
  { key: 'wind_speed_ms', label: 'Wind speed', short: 'W', quantity: 'speed', digits: 1 },
  { key: 'wind_gust_ms', label: 'Wind gust', short: 'WG', quantity: 'speed', digits: 1 },
  { key: 'wind_dir_deg', label: 'Wind direction', short: 'WD', quantity: 'direction', digits: 0 },
  { key: 'rain_mm', label: 'Rain total', short: 'R', quantity: 'rain', digits: 1 },
  { key: 'uv_index', label: 'UV index', short: 'UV', quantity: 'index', digits: 1 },
  { key: 'light_lux', label: 'Light', short: 'Lx', quantity: 'lux', digits: 0 },
  { key: 'solar_wm2', label: 'Solar', short: 'S', quantity: 'irradiance', digits: 0 },
  { key: 'lightning_count', label: 'Lightning strikes', short: 'Ln', quantity: 'count', digits: 0 },
  {
    key: 'lightning_km',
    label: 'Lightning distance',
    short: 'Ld',
    quantity: 'distance',
    digits: 0,
  },
  { key: 'battery_ok', label: 'Battery', short: 'B', quantity: 'boolean', digits: 0 },
];

const FIELD_BY_KEY: Record<string, FieldMeta> = Object.fromEntries(FIELDS.map((f) => [f.key, f]));

export function fieldMeta(key: string): FieldMeta {
  return (
    FIELD_BY_KEY[key] ?? {
      key,
      label: key.replace(/_/g, ' '),
      short: key.slice(0, 2).toUpperCase(),
      quantity: 'count',
      digits: 2,
    }
  );
}

/** Convert an SI value into the display value for the chosen system. */
export function convert(value: number, quantity: Quantity, system: UnitSystem): number {
  if (system === 'metric') {
    if (quantity === 'speed') return value * 3.6; // m/s to km/h
    return value;
  }
  switch (quantity) {
    case 'temperature':
      return value * 1.8 + 32;
    case 'speed':
      return value * 2.2369362921; // m/s to mph
    case 'pressure':
      return value * 0.0295299830714; // hPa to inHg
    case 'rain':
      return value / 25.4; // mm to in
    case 'distance':
      return value * 0.621371192; // km to mi
    case 'length':
      return value * 3.280839895; // m to ft
    default:
      return value;
  }
}

/** Unit label for a quantity in the chosen system. */
export function unitLabel(quantity: Quantity, system: UnitSystem): string {
  const metric: Record<Quantity, string> = {
    temperature: '°C',
    speed: 'km/h',
    pressure: 'hPa',
    rain: 'mm',
    distance: 'km',
    length: 'm',
    percent: '%',
    direction: '°',
    index: '',
    lux: 'lx',
    irradiance: 'W/m²',
    count: '',
    boolean: '',
  };
  const us: Partial<Record<Quantity, string>> = {
    temperature: '°F',
    speed: 'mph',
    pressure: 'inHg',
    rain: 'in',
    distance: 'mi',
    length: 'ft',
  };
  return system === 'us' ? (us[quantity] ?? metric[quantity]) : metric[quantity];
}

/** Decimal places that make sense after conversion. */
export function displayDigits(meta: FieldMeta, system: UnitSystem): number {
  if (system === 'us') {
    if (meta.quantity === 'pressure') return 2;
    if (meta.quantity === 'rain') return 2;
    if (meta.quantity === 'temperature') return 0;
  }
  return meta.digits;
}

const COMPASS = [
  'N',
  'NNE',
  'NE',
  'ENE',
  'E',
  'ESE',
  'SE',
  'SSE',
  'S',
  'SSW',
  'SW',
  'WSW',
  'W',
  'WNW',
  'NW',
  'NNW',
];

export function compassPoint(deg: number): string {
  const idx = Math.round((((deg % 360) + 360) % 360) / 22.5) % 16;
  return COMPASS[idx];
}

/** Render a field value as text, with unit, for the chosen system. */
export function formatField(key: string, value: number, system: UnitSystem): string {
  const meta = fieldMeta(key);
  if (meta.quantity === 'boolean') return value ? 'OK' : 'Low';
  if (meta.quantity === 'direction') return `${Math.round(value)}° ${compassPoint(value)}`;
  const v = convert(value, meta.quantity, system);
  const unit = unitLabel(meta.quantity, system);
  const text = v.toFixed(displayDigits(meta, system));
  return unit ? `${text} ${unit}` : text;
}

export function formatValue(
  value: number,
  quantity: Quantity,
  system: UnitSystem,
  digits = 1,
): string {
  const v = convert(value, quantity, system);
  const unit = unitLabel(quantity, system);
  return unit ? `${v.toFixed(digits)} ${unit}` : v.toFixed(digits);
}

export function readUnitSystem(storage: Pick<Storage, 'getItem'> | null): UnitSystem {
  try {
    const v = storage?.getItem(UNIT_STORAGE_KEY);
    return v === 'us' ? 'us' : 'metric';
  } catch {
    return 'metric';
  }
}

export function writeUnitSystem(
  storage: Pick<Storage, 'setItem'> | null,
  system: UnitSystem,
): void {
  try {
    storage?.setItem(UNIT_STORAGE_KEY, system);
  } catch {
    // Storage can be unavailable in private windows. The choice just will not stick.
  }
}
