import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Field, FilterChip, PageHeader, SummaryGrid, SummaryMetric } from './ui';

describe('admin UI primitives', () => {
  it('renders a page header with description and actions', () => {
    render(
      <PageHeader
        actions={<a href="/provider-keys">Manage Provider Endpoints</a>}
        description="Inference readiness and per-endpoint quota."
        title="Provider Monitoring"
      />,
    );
    expect(screen.getByRole('heading', { name: 'Provider Monitoring' })).toBeInTheDocument();
    expect(screen.getByText(/inference readiness/i)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /manage provider endpoints/i })).toHaveAttribute('href', '/provider-keys');
  });

  it('renders summary values and omits metrics the caller does not pass', () => {
    render(
      <SummaryGrid>
        <SummaryMetric label="Enabled" testId="enabled-metric" value={2} />
        <SummaryMetric label="Cooling" tone="warn" value={1} />
      </SummaryGrid>,
    );
    expect(screen.getByTestId('enabled-metric')).toHaveTextContent('Enabled');
    expect(screen.getByTestId('enabled-metric')).toHaveTextContent('2');
    expect(screen.getByText('Cooling')).toBeInTheDocument();
    expect(screen.queryByText('Known remaining')).not.toBeInTheDocument();
  });

  it('exposes filter state through aria-pressed and calls onClick', () => {
    const onClick = vi.fn();
    render(<FilterChip active ariaLabel="Show active Grok pool" count={2} label="Active pool" onClick={onClick} />);
    const chip = screen.getByRole('button', { name: 'Show active Grok pool' });
    expect(chip).toHaveAttribute('aria-pressed', 'true');
    expect(chip).toHaveTextContent('Active pool');
    expect(chip).toHaveTextContent('2');
    fireEvent.click(chip);
    expect(onClick).toHaveBeenCalledOnce();
  });

  it('associates Field helper text with the input', () => {
    render(<Field hint="Copied only into newly created endpoints." label="Default base URL" />);
    const input = screen.getByLabelText('Default base URL');
    const hint = screen.getByText(/copied only into newly created endpoints/i);
    expect(input).toHaveAttribute('aria-describedby', hint.id);
  });
});
