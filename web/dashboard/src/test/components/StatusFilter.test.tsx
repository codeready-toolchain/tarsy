import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StatusFilter } from '../../components/dashboard/StatusFilter';
import { SESSION_STATUS } from '../../constants/sessionStatus';

describe('StatusFilter', () => {
  it('clears the selection from All', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <StatusFilter
        value={[SESSION_STATUS.COMPLETED]}
        onChange={onChange}
        options={[SESSION_STATUS.COMPLETED, SESSION_STATUS.FAILED]}
      />,
    );

    await user.click(screen.getByRole('combobox', { name: 'Status' }));
    await user.click(screen.getByRole('option', { name: 'All' }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it('removes a status from the chip delete button', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <StatusFilter
        value={[SESSION_STATUS.COMPLETED]}
        onChange={onChange}
        options={[SESSION_STATUS.COMPLETED, SESSION_STATUS.FAILED]}
      />,
    );

    await user.click(screen.getByTestId('CancelIcon'));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it('deselects a status when its menu item is clicked again', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <StatusFilter
        value={[SESSION_STATUS.COMPLETED]}
        onChange={onChange}
        options={[SESSION_STATUS.COMPLETED, SESSION_STATUS.FAILED]}
      />,
    );

    await user.click(screen.getByRole('combobox', { name: 'Status' }));
    await user.click(screen.getByRole('option', { name: /Completed/ }));
    expect(onChange).toHaveBeenCalledWith([]);
  });
});
