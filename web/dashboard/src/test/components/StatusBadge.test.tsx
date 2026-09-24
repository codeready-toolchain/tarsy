import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StatusBadge } from '../../components/common/StatusBadge';

describe('StatusBadge', () => {
  it('shows tooltip on hover when provided', async () => {
    const user = userEvent.setup();
    render(<StatusBadge status="cancelled" tooltip="Cancelled by alice@example.com" />);

    await user.hover(screen.getByText('Cancelled'));
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Cancelled by alice@example.com',
    );
  });

  it('does not render a tooltip when omitted', async () => {
    const user = userEvent.setup();
    render(<StatusBadge status="cancelled" />);

    await user.hover(screen.getByText('Cancelled'));
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
  });
});
