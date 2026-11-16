import { useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from 'react';

interface FieldShellProps {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  id: string;
  children: ReactNode;
  inline?: boolean;
}

function FieldShell({ label, hint, error, id, children, inline }: FieldShellProps) {
  return (
    <div className={`field${inline ? ' field-inline' : ''}${error ? ' field-error' : ''}`}>
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      {children}
      {hint && (
        <div className="field-hint" id={`${id}-hint`}>
          {hint}
        </div>
      )}
      {error && (
        <div className="field-error-text" id={`${id}-error`} role="alert">
          {error}
        </div>
      )}
    </div>
  );
}

function describedBy(id: string, hint?: ReactNode, error?: ReactNode): string | undefined {
  const ids = [hint ? `${id}-hint` : null, error ? `${id}-error` : null].filter(Boolean);
  return ids.length ? ids.join(' ') : undefined;
}

interface TextFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  id?: string;
}

export function TextField({ label, hint, error, id: givenId, ...rest }: TextFieldProps) {
  const autoId = useId();
  const id = givenId ?? autoId;
  return (
    <FieldShell label={label} hint={hint} error={error} id={id}>
      <input
        id={id}
        className="input"
        aria-describedby={describedBy(id, hint, error)}
        aria-invalid={error ? true : undefined}
        {...rest}
      />
    </FieldShell>
  );
}

interface NumberFieldProps extends Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'id' | 'value' | 'onChange' | 'type'
> {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  id?: string;
  value: number | null;
  onChange: (value: number | null) => void;
  /** Text appended after the input, for example the unit. */
  suffix?: ReactNode;
}

export function NumberField({
  label,
  hint,
  error,
  id: givenId,
  value,
  onChange,
  suffix,
  ...rest
}: NumberFieldProps) {
  const autoId = useId();
  const id = givenId ?? autoId;
  return (
    <FieldShell label={label} hint={hint} error={error} id={id}>
      <div className="input-group">
        <input
          id={id}
          className="input"
          type="number"
          inputMode="decimal"
          value={value === null ? '' : String(value)}
          onChange={(e) => {
            const raw = e.target.value;
            if (raw === '') return onChange(null);
            const n = Number(raw);
            onChange(Number.isFinite(n) ? n : null);
          }}
          aria-describedby={describedBy(id, hint, error)}
          aria-invalid={error ? true : undefined}
          {...rest}
        />
        {suffix && <span className="input-suffix">{suffix}</span>}
      </div>
    </FieldShell>
  );
}

interface SelectFieldProps<V extends string | number> extends Omit<
  SelectHTMLAttributes<HTMLSelectElement>,
  'id' | 'value' | 'onChange'
> {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  id?: string;
  value: V;
  options: { value: V; label: string }[];
  onChange: (value: V) => void;
}

export function SelectField<V extends string | number>({
  label,
  hint,
  error,
  id: givenId,
  value,
  options,
  onChange,
  ...rest
}: SelectFieldProps<V>) {
  const autoId = useId();
  const id = givenId ?? autoId;
  return (
    <FieldShell label={label} hint={hint} error={error} id={id}>
      <select
        id={id}
        className="input"
        value={String(value)}
        onChange={(e) => {
          const picked = options.find((o) => String(o.value) === e.target.value);
          if (picked) onChange(picked.value);
        }}
        aria-describedby={describedBy(id, hint, error)}
        {...rest}
      >
        {options.map((o) => (
          <option key={String(o.value)} value={String(o.value)}>
            {o.label}
          </option>
        ))}
      </select>
    </FieldShell>
  );
}

interface CheckboxFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id' | 'type'> {
  label: ReactNode;
  hint?: ReactNode;
  id?: string;
}

export function CheckboxField({ label, hint, id: givenId, ...rest }: CheckboxFieldProps) {
  const autoId = useId();
  const id = givenId ?? autoId;
  return (
    <div className="field field-checkbox">
      <input id={id} type="checkbox" aria-describedby={hint ? `${id}-hint` : undefined} {...rest} />
      <label htmlFor={id}>{label}</label>
      {hint && (
        <div className="field-hint" id={`${id}-hint`}>
          {hint}
        </div>
      )}
    </div>
  );
}

/** A group of radio buttons that behaves like a single select. */
interface RadioGroupProps<V extends string> {
  legend: ReactNode;
  name: string;
  value: V;
  options: { value: V; label: string; hint?: string }[];
  onChange: (value: V) => void;
}

export function RadioGroup<V extends string>({
  legend,
  name,
  value,
  options,
  onChange,
}: RadioGroupProps<V>) {
  return (
    <fieldset className="field radio-group">
      <legend className="field-label">{legend}</legend>
      {options.map((o) => {
        const id = `${name}-${o.value}`;
        return (
          <div key={o.value} className="radio-option">
            <input
              type="radio"
              id={id}
              name={name}
              value={o.value}
              checked={value === o.value}
              onChange={() => onChange(o.value)}
            />
            <label htmlFor={id}>
              {o.label}
              {o.hint && <span className="muted"> {o.hint}</span>}
            </label>
          </div>
        );
      })}
    </fieldset>
  );
}
