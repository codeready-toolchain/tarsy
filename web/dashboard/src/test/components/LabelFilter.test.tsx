import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { LabelFilter } from '../../components/dashboard/LabelFilter';

describe('LabelFilter', () => {
  it('renders selected labels as colored chips', () => {
    render(<LabelFilter value={['page']} onChange={() => {}} options={['page', 'watch']} />);
    expect(screen.getByText('page').closest('.MuiChip-root')).toHaveClass('MuiChip-colorError');
  });

  it('shows label chips in the menu', async () => {
    const user = userEvent.setup();
    render(<LabelFilter value={[]} onChange={() => {}} options={['page', 'watch', 'noise']} />);

    await user.click(screen.getByRole('combobox', { name: 'Label' }));

    expect(screen.getByRole('option', { name: 'All' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /page/ }).querySelector('.MuiChip-root')).toHaveClass(
      'MuiChip-colorError',
    );
    expect(screen.getByRole('option', { name: /watch/ }).querySelector('.MuiChip-root')).toHaveClass(
      'MuiChip-colorInfo',
    );
    expect(screen.getByRole('option', { name: /noise/ }).querySelector('.MuiChip-root')).toHaveAttribute(
      'data-muted',
      'true',
    );
  });

  it('clears the selection from All', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<LabelFilter value={['page']} onChange={onChange} options={['page', 'watch']} />);

    await user.click(screen.getByRole('combobox', { name: 'Label' }));
    await user.click(screen.getByRole('option', { name: 'All' }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it('removes a label from the chip delete button', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<LabelFilter value={['page']} onChange={onChange} options={['page', 'watch']} />);

    await user.click(screen.getByTestId('CancelIcon'));
    expect(onChange).toHaveBeenCalledWith([]);
  });
});
