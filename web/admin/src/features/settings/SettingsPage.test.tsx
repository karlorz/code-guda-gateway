import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { SettingsPage } from './SettingsPage';

describe('SettingsPage', () => {
  it('shows authoritative runtime and endpoint-default guidance', () => {
    render(<MemoryRouter><SettingsPage /></MemoryRouter>);
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getByText('/admin')).toBeInTheDocument();
    expect(screen.getByText(/defaults apply only to newly created endpoints/i)).toBeInTheDocument();
    expect(screen.getByText(/never used as an inference fallback/i)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /manage provider endpoints/i })).toHaveAttribute('href', '/provider-keys');
  });

  it('does not expose tabs, fake inputs, or a save bar', () => {
    render(<MemoryRouter><SettingsPage /></MemoryRouter>);
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/unsaved changes/i)).not.toBeInTheDocument();
  });
});
