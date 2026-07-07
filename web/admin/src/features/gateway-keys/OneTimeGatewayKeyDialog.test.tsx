import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { OneTimeGatewayKeyDialog } from './OneTimeGatewayKeyDialog';

describe('OneTimeGatewayKeyDialog', () => {
  it('requires saved acknowledgement before close', () => {
    const onClose = vi.fn();
    render(<OneTimeGatewayKeyDialog rawKey="gsk_example" onClose={onClose} />);

    expect(screen.getByDisplayValue('gsk_example')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /done/i }));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByLabelText(/i have saved/i));
    fireEvent.click(screen.getByRole('button', { name: /done/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
