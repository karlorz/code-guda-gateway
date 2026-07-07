import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react';
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

export function Field({ label, ...props }: InputHTMLAttributes<HTMLInputElement> & { label: string }) {
  return (
    <label className="grid gap-1 text-sm font-medium text-zinc-700">
      <span>{label}</span>
      <input
        className="h-9 rounded-md border border-zinc-300 bg-white px-3 text-sm text-zinc-950 outline-none focus:border-zinc-500"
        {...props}
      />
    </label>
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
      <div className="w-full max-w-lg rounded-lg bg-white p-5 shadow-xl">
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
