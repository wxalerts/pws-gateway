import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import { readUnitSystem, writeUnitSystem, type UnitSystem } from './units';

interface UnitsContextValue {
  system: UnitSystem;
  setSystem: (s: UnitSystem) => void;
}

const UnitsContext = createContext<UnitsContextValue>({
  system: 'metric',
  setSystem: () => {},
});

function storage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null;
  } catch {
    return null;
  }
}

export function UnitsProvider({ children }: { children: ReactNode }) {
  const [system, setSystemState] = useState<UnitSystem>(() => readUnitSystem(storage()));
  const setSystem = useCallback((s: UnitSystem) => {
    setSystemState(s);
    writeUnitSystem(storage(), s);
  }, []);
  const value = useMemo(() => ({ system, setSystem }), [system, setSystem]);
  return <UnitsContext.Provider value={value}>{children}</UnitsContext.Provider>;
}

export function useUnits(): UnitsContextValue {
  return useContext(UnitsContext);
}
