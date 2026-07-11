import { useId, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from 'react';
import { X } from 'lucide-react';

export function Button({
  className = '',
  variant = 'primary',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'danger' }) {
  const styles = {
    primary: 'bg-zinc-950 text-white hover:bg-zinc-800',
    secondary: 'border border-zinc-300 bg-white text-zinc-900 hover:bg-zinc-100',
    danger: 'bg-red-600 text-white hover:bg-red-700',
  };
  return (
    <button
      className={`inline-flex h-9 items-center justify-center gap-2 rounded-md px-3 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${styles[variant]} ${className}`}
      {...props}
    />
  );
}

export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'good' | 'warn' | 'bad' }) {
  const styles = {
    neutral: 'bg-zinc-100 text-zinc-700',
    good: 'bg-emerald-50 text-emerald-700',
    warn: 'bg-amber-50 text-amber-700',
    bad: 'bg-red-50 text-red-700',
  };
  return <span className={`inline-flex rounded px-2 py-1 text-xs font-medium ${styles[tone]}`}>{children}</span>;
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-2xl font-semibold text-zinc-950">{title}</h1>
        {description ? <p className="mt-1 max-w-3xl text-sm text-zinc-600">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}

export function SummaryGrid({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`grid overflow-hidden rounded-lg border border-zinc-200 bg-white sm:grid-cols-2 ${className}`}>
      {children}
    </div>
  );
}

export function SummaryMetric({
  label,
  value,
  tone = 'neutral',
  testId,
}: {
  label: string;
  value: ReactNode;
  tone?: 'neutral' | 'good' | 'warn' | 'bad';
  testId?: string;
}) {
  const valueStyle = {
    neutral: 'text-zinc-950',
    good: 'text-emerald-700',
    warn: 'text-amber-700',
    bad: 'text-red-700',
  }[tone];
  return (
    <div className="border-b border-r border-zinc-200 px-3 py-2.5 last:border-r-0" data-testid={testId}>
      <div className="text-[11px] font-medium uppercase tracking-wide text-zinc-500">{label}</div>
      <div className={`mt-1 text-xl font-semibold tabular-nums ${valueStyle}`}>{value}</div>
    </div>
  );
}

export function FilterChip({
  label,
  count,
  active,
  onClick,
  ariaLabel,
}: {
  label: string;
  count?: number;
  active: boolean;
  onClick: () => void;
  ariaLabel?: string;
}) {
  return (
    <button
      aria-label={ariaLabel}
      aria-pressed={active}
      className={`inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-xs font-medium transition ${
        active
          ? 'border-zinc-950 bg-zinc-950 text-white'
          : 'border-zinc-300 bg-white text-zinc-700 hover:bg-zinc-100'
      }`}
      onClick={onClick}
      type="button"
    >
      <span>{label}</span>
      {count != null ? (
        <span className={active ? 'text-zinc-300' : 'text-zinc-500'}>{count}</span>
      ) : null}
    </button>
  );
}

export function Field({
  label,
  hint,
  id,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string }) {
  const generatedID = useId();
  const inputID = id ?? `${generatedID}-input`;
  const hintID = hint ? `${inputID}-hint` : undefined;
  return (
    <div className="grid gap-1 text-sm font-medium text-zinc-700">
      <label htmlFor={inputID}>
        <span>{label}</span>
      </label>
      <input
        aria-describedby={hintID}
        className="h-9 rounded-md border border-zinc-300 bg-white px-3 text-sm text-zinc-950 outline-none focus:border-zinc-500"
        id={inputID}
        {...props}
      />
      {hint ? <span className="text-xs font-normal text-zinc-500" id={hintID}>{hint}</span> : null}
    </div>
  );
}


export function Panel({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }) {
  return (
    <section className="grid gap-3 border-t border-zinc-200 py-5">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold text-zinc-950">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}

export function Dialog({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-zinc-950/30 p-4">
      <div
        aria-label={title}
        aria-modal="true"
        className="w-full max-w-lg rounded-lg bg-white p-5 shadow-xl"
        role="dialog"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button aria-label="Close" className="rounded p-1 hover:bg-zinc-100" onClick={onClose} type="button">
            <X size={18} />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

export function valueOf<T>(row: Record<string, unknown>, pascal: string, snake: string, fallback: T): T {
  return (row[snake] ?? row[pascal] ?? fallback) as T;
}
