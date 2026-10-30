import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { NotificationLabelField } from '../../components/settings/NotificationLabelField';
import { NotificationMasterSwitch } from '../../components/layout/NotificationMasterSwitch';
import { NotificationPermissionPrompt } from '../../components/layout/NotificationPermissionPrompt';
import { SettingsPage } from '../../pages/SettingsPage';
import { NotificationSettingsProvider, useNotificationSettings } from '../../contexts/NotificationSettingsContext';
import { PageHeaderProvider } from '../../contexts/PageHeaderContext';
import { defaultNotificationSettings, LABEL_ALL, LABEL_ANY, LABEL_NONE, saveNotificationSettings } from '../../utils/notificationSettings';

vi.mock('../../services/api.ts', () => ({
  getSystemConfig: vi.fn().mockResolvedValue({
    label_maps: {
      builtin: { labels: [{ label: 'watch' }, { label: 'action' }, { label: 'noise' }] },
    },
  }),
  getFilterOptions: vi.fn().mockResolvedValue({ labels: ['action', 'stored'] }),
}));

vi.mock('../../contexts/VersionContext.tsx', () => ({
  useVersion: () => ({ backendVersion: 'test', backendStatus: 'ok' }),
}));

function renderSettings() {
  return render(
    <MemoryRouter>
      <PageHeaderProvider>
        <NotificationSettingsProvider>
          <SettingsPage />
        </NotificationSettingsProvider>
      </PageHeaderProvider>
    </MemoryRouter>,
  );
}

describe('notification controls', () => {
  afterEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('asks for notification permission when the dashboard opens', () => {
    const requestPermission = vi.fn().mockResolvedValue('default');
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    render(
      <NotificationSettingsProvider>
        <NotificationPermissionPrompt />
      </NotificationSettingsProvider>,
    );
    expect(requestPermission).toHaveBeenCalledOnce();
  });

  it('does not ask when the browser has already been answered', () => {
    const requestPermission = vi.fn();
    vi.stubGlobal('Notification', { permission: 'granted', requestPermission });
    render(
      <NotificationSettingsProvider>
        <NotificationPermissionPrompt />
      </NotificationSettingsProvider>,
    );
    expect(requestPermission).not.toHaveBeenCalled();
  });

  it('does not ask on open when desktop notifications are off', () => {
    const requestPermission = vi.fn();
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    saveNotificationSettings({
      master: true,
      tabCounter: true,
      desktop: false,
      labels: ['ban', 'action'],
    });
    render(
      <NotificationSettingsProvider>
        <NotificationPermissionPrompt />
      </NotificationSettingsProvider>,
    );
    expect(requestPermission).not.toHaveBeenCalled();
  });

  it('does not ask for notification permission from the settings page itself', () => {
    const requestPermission = vi.fn();
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    renderSettings();
    expect(requestPermission).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Allow notifications' })).toBeInTheDocument();
  });

  it('asks from the settings button and says when the browser is blocking toasts', async () => {
    const user = userEvent.setup();
    const requestPermission = vi.fn().mockResolvedValue('granted');
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    const { unmount } = renderSettings();

    await user.click(screen.getByRole('button', { name: 'Allow notifications' }));
    expect(requestPermission).toHaveBeenCalledOnce();
    expect(await screen.findByText('Desktop notifications are allowed.')).toBeInTheDocument();

    unmount();
    vi.stubGlobal('Notification', { permission: 'denied', requestPermission: vi.fn() });
    renderSettings();
    expect(screen.getByText('The browser is blocking toasts.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Allow notifications' })).not.toBeInTheDocument();
  });

  it('shows both signals paused while the header switch is off', () => {
    saveNotificationSettings({
      master: false,
      tabCounter: true,
      desktop: true,
      labels: ['ban', 'action'],
    });
    renderSettings();

    expect(screen.getByRole('switch', { name: 'Tab counter' })).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Tab counter' })).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Desktop notifications' })).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Desktop notifications' })).toBeDisabled();
    expect(screen.getByText(/The header switch is off, so both signals are paused/)).toBeInTheDocument();

    const saved = JSON.parse(localStorage.getItem('tarsy-notification-settings') ?? '{}') as { tabCounter: boolean; desktop: boolean };
    expect(saved.tabCounter).toBe(true);
    expect(saved.desktop).toBe(true);
  });

  it('resets the switches and labels to the defaults', async () => {
    const user = userEvent.setup();
    saveNotificationSettings({
      master: false,
      tabCounter: false,
      desktop: false,
      labels: ['watch'],
    });
    renderSettings();

    expect(screen.getByRole('switch', { name: 'Tab counter' })).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Desktop notifications' })).not.toBeChecked();
    expect(screen.getByRole('button', { name: 'watch' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Reset to defaults' }));

    expect(screen.getByRole('switch', { name: 'Tab counter' })).toBeChecked();
    expect(screen.getByRole('switch', { name: 'Desktop notifications' })).toBeChecked();
    expect(screen.getByRole('button', { name: 'ban' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'action' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'watch' })).not.toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem('tarsy-notification-settings') ?? '{}')).toEqual(defaultNotificationSettings());
    expect(screen.getByRole('button', { name: 'Reset to defaults' })).toBeDisabled();
  });

  it('replaces names with Any label in the label field', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <NotificationLabelField
        value={['ban', 'action']}
        options={['action', 'ban', 'watch']}
        onChange={onChange}
      />,
    );

    await user.click(screen.getByRole('combobox', { name: 'Labels' }));
    await user.click(screen.getByRole('option', { name: 'Any label' }));
    expect(onChange).toHaveBeenCalledWith([LABEL_ANY]);
  });

  it('replaces the selection with All sessions', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <NotificationLabelField value={['ban', 'action']} options={['action', 'ban']} onChange={onChange} />,
    );

    await user.click(screen.getByRole('combobox', { name: 'Labels' }));
    await user.click(screen.getByRole('option', { name: 'All sessions' }));
    expect(onChange).toHaveBeenCalledWith([LABEL_ALL]);
  });

  it('selects All sessions from the clear button and leaves a removed chip empty', async () => {
    const user = userEvent.setup();
    const onClear = vi.fn();
    const { rerender } = render(
      <NotificationLabelField value={['ban', 'action']} options={['action', 'ban']} onChange={onClear} />,
    );

    fireEvent.click(screen.getByTitle('Clear'));
    expect(onClear).toHaveBeenCalledWith([LABEL_ALL]);

    const onRemove = vi.fn();
    rerender(<NotificationLabelField value={['ban']} options={['ban']} onChange={onRemove} />);
    await user.click(screen.getByTestId('CancelIcon'));
    expect(onRemove).toHaveBeenCalledWith([]);
  });

  it('stacks No label with a name', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <NotificationLabelField value={['ban']} options={['ban']} onChange={onChange} />,
    );

    await user.click(screen.getByRole('combobox', { name: 'Labels' }));
    await user.click(screen.getByRole('option', { name: 'No label' }));
    expect(onChange).toHaveBeenCalledWith(['ban', LABEL_NONE]);
  });

  it('commits a typed label and hides an invalid token', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <NotificationLabelField value={[LABEL_ANY, LABEL_NONE]} options={[]} onChange={onChange} />,
    );

    const input = screen.getByRole('combobox', { name: 'Labels' });
    await user.type(input, '1bad');
    expect(screen.queryByRole('option', { name: '1bad' })).not.toBeInTheDocument();

    await user.clear(input);
    await user.type(input, 'watch{Enter}');
    expect(onChange).toHaveBeenCalledWith(['watch', LABEL_NONE]);
  });

  it('toggles the master switch without editing labels and shows the backlog count', async () => {
    const user = userEvent.setup();
    const requestPermission = vi.fn().mockResolvedValue('granted');
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    saveNotificationSettings({
      master: false,
      tabCounter: true,
      desktop: true,
      labels: ['ban'],
    });

    function Harness() {
      const { setBacklogCount } = useNotificationSettings();
      return (
        <>
          <button type="button" onClick={() => setBacklogCount(12)}>set count</button>
          <button type="button" onClick={() => setBacklogCount(0)}>clear count</button>
          <NotificationMasterSwitch />
        </>
      );
    }

    render(
      <NotificationSettingsProvider>
        <Harness />
      </NotificationSettingsProvider>,
    );

    expect(requestPermission).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Review notifications off' }));
    expect(requestPermission).toHaveBeenCalledOnce();

    const saved = JSON.parse(localStorage.getItem('tarsy-notification-settings') ?? '{}') as { labels: string[]; master: boolean };
    expect(saved.master).toBe(true);
    expect(saved.labels).toEqual(['ban']);

    await user.click(screen.getByRole('button', { name: 'set count' }));
    expect(screen.getByRole('button', { name: 'Review notifications on, 12 waiting' })).toHaveTextContent('12');

    await user.click(screen.getByRole('button', { name: 'clear count' }));
    const cleared = screen.getByRole('button', { name: 'Review notifications on' });
    expect(cleared).not.toHaveTextContent('0');
  });

  it('does not ask for permission when desktop notifications are off', async () => {
    const user = userEvent.setup();
    const requestPermission = vi.fn();
    vi.stubGlobal('Notification', { permission: 'default', requestPermission });
    saveNotificationSettings({
      master: false,
      tabCounter: true,
      desktop: false,
      labels: ['ban', 'action'],
    });

    render(
      <NotificationSettingsProvider>
        <NotificationMasterSwitch />
      </NotificationSettingsProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'Review notifications off' }));
    expect(requestPermission).not.toHaveBeenCalled();
  });
});
