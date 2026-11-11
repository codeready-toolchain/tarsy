import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FilterPanel } from '../../components/dashboard/FilterPanel';
import { getDefaultFilters } from '../../utils/filterPersistence';

describe('FilterPanel post-investigation filter', () => {
  it('sets the filter from the select', async () => {
    const user = userEvent.setup();
    const onFiltersChange = vi.fn();
    render(
      <FilterPanel filters={getDefaultFilters()} onFiltersChange={onFiltersChange} onClearFilters={() => {}} />,
    );

    await user.click(screen.getByRole('combobox', { name: 'Post-investigation' }));
    await user.click(screen.getByRole('option', { name: 'Acted' }));

    expect(onFiltersChange).toHaveBeenCalledWith({ ...getDefaultFilters(), post_investigation: 'acted' });
  });

  it('shows an active chip that clears the filter', async () => {
    const user = userEvent.setup();
    const onFiltersChange = vi.fn();
    render(
      <FilterPanel
        filters={{ ...getDefaultFilters(), post_investigation: 'none' }}
        onFiltersChange={onFiltersChange}
        onClearFilters={() => {}}
      />,
    );

    expect(screen.getByText('Active Filters (1):')).toBeInTheDocument();
    const chip = screen.getByText('Post-investigation: None').closest('.MuiChip-root') as HTMLElement;
    await user.click(within(chip).getByTestId('CancelIcon'));

    expect(onFiltersChange).toHaveBeenCalledWith({ ...getDefaultFilters(), post_investigation: '' });
  });
});
