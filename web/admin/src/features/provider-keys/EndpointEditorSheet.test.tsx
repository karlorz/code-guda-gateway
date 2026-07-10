import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi, afterEach } from 'vitest';
import { EndpointEditorSheet } from './EndpointEditorSheet';

describe('EndpointEditorSheet', () => {
  afterEach(() => {
    cleanup();
  });

  it('defaults Grok quota to disabled', () => {
    render(
      <EndpointEditorSheet
        mode="create"
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByLabelText(/quota mode/i)).toHaveValue('disabled');
  });

  it('defaults Tavily and Firecrawl quota to endpoint credentials', () => {
    const { rerender } = render(
      <EndpointEditorSheet mode="create" onClose={vi.fn()} onCreate={vi.fn()} />,
    );
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/^provider$/i), { target: { value: 'tavily' } });
    expect(within(dialog).getByLabelText(/quota mode/i)).toHaveValue('endpoint_credentials');
    expect(within(dialog).getByLabelText(/quota flow/i)).toHaveValue('tavily_usage');

    fireEvent.change(within(dialog).getByLabelText(/^provider$/i), { target: { value: 'firecrawl' } });
    expect(within(dialog).getByLabelText(/quota mode/i)).toHaveValue('endpoint_credentials');
    expect(within(dialog).getByLabelText(/quota flow/i)).toHaveValue('firecrawl_credit_usage');

    // preserve manual mode when provider changes after operator touch
    fireEvent.change(within(dialog).getByLabelText(/quota mode/i), { target: { value: 'disabled' } });
    fireEvent.change(within(dialog).getByLabelText(/^provider$/i), { target: { value: 'tavily' } });
    expect(within(dialog).getByLabelText(/quota mode/i)).toHaveValue('disabled');

    rerender(<EndpointEditorSheet mode="create" onClose={vi.fn()} onCreate={vi.fn()} />);
  });

  it('reveals quota URL and key only for separate credentials', () => {
    render(<EndpointEditorSheet mode="create" onClose={vi.fn()} onCreate={vi.fn()} />);
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).queryByLabelText(/quota base url/i)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^quota key$/i)).not.toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText(/quota mode/i), {
      target: { value: 'separate_credentials' },
    });
    expect(within(dialog).getByLabelText(/quota base url/i)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^quota key$/i)).toHaveAttribute('type', 'password');
    expect(within(dialog).getByLabelText(/^quota key$/i)).toHaveAttribute('autocomplete', 'off');
  });

  it('never prefills inference or quota secrets while editing', () => {
    render(
      <EndpointEditorSheet
        endpoint={{
          ID: 42,
          Provider: 'grok',
          Name: 'primary',
          BaseURL: 'https://api.x.ai/v1',
          KeyPrefix: 'xai-secret-prefix',
          QuotaMode: 'separate_credentials',
          QuotaFlow: 'grok2api_admin',
          QuotaBaseURL: 'https://grok2api.example',
          QuotaKeyConfigured: true,
          QuotaKeyPrefix: 'g2a-',
        }}
        mode="edit"
        onClose={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );
    const dialog = screen.getByRole('dialog', { name: /edit endpoint/i });
    const secretFields = within(dialog).getAllByDisplayValue('');
    // inference + quota password fields start empty
    const passwords = within(dialog)
      .getAllByLabelText(/key/i)
      .filter((el) => (el as HTMLInputElement).type === 'password');
    expect(passwords.length).toBeGreaterThanOrEqual(1);
    for (const field of passwords) {
      expect(field).toHaveValue('');
    }
    expect(within(dialog).queryByDisplayValue(/xai-|g2a-|secret/i)).not.toBeInTheDocument();
    expect(secretFields.length).toBeGreaterThan(0);
  });

  it('explains that endpoint name does not define routing priority', () => {
    render(<EndpointEditorSheet mode="create" onClose={vi.fn()} onCreate={vi.fn()} />);
    expect(screen.getByText(/does not define routing priority/i)).toBeInTheDocument();
  });
});
