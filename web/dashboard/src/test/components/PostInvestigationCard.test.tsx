import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import PostInvestigationCard from '../../components/session/PostInvestigationCard';
import type { StageOverview, TimelineEvent } from '../../types/session';
import { STAGE_TYPE, TIMELINE_EVENT_TYPES, TIMELINE_STATUS } from '../../constants/eventTypes';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

function makeStage(
  id: string,
  name: string,
  index: number,
  overrides: Partial<StageOverview> = {},
): StageOverview {
  return {
    id,
    stage_name: name,
    stage_index: index,
    stage_type: STAGE_TYPE.POST_INVESTIGATION,
    status: EXECUTION_STATUS.COMPLETED,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:06:00Z',
    completed_at: '2025-01-15T10:07:00Z',
    ...overrides,
  };
}

function makeMemo(
  id: string,
  stageId: string,
  content: string,
  overrides: Partial<TimelineEvent> = {},
): TimelineEvent {
  return {
    id,
    session_id: 'session-1',
    stage_id: stageId,
    execution_id: `exec-${stageId}`,
    sequence_number: 1,
    event_type: TIMELINE_EVENT_TYPES.FINAL_ANALYSIS,
    status: TIMELINE_STATUS.COMPLETED,
    content,
    metadata: null,
    created_at: '2025-01-15T10:07:00Z',
    updated_at: '2025-01-15T10:07:00Z',
    ...overrides,
  };
}

describe('PostInvestigationCard', () => {
  it('renders nothing without post-investigation stages', () => {
    const { container } = render(
      <PostInvestigationCard
        stages={[makeStage('s1', 'Investigation', 0, { stage_type: STAGE_TYPE.INVESTIGATION })]}
        timelineEvents={[]}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders entries in stage order with agent, status and outcome', () => {
    render(
      <PostInvestigationCard
        stages={[
          makeStage('s3', 'notify', 3, { actions_executed: false }),
          makeStage('s2', 'file-ticket', 2, {
            actions_executed: true,
            executions: [
              {
                execution_id: 'exec-s2',
                agent_name: 'TicketFiler',
                agent_index: 1,
                status: EXECUTION_STATUS.COMPLETED,
                llm_backend: 'test',
                llm_provider: 'test',
                started_at: null,
                completed_at: null,
                duration_ms: null,
                error_message: null,
                input_tokens: 0,
                output_tokens: 0,
                total_tokens: 0,
              },
            ],
          }),
          makeStage('s4', 'follow-up', 4, { status: EXECUTION_STATUS.FAILED }),
        ]}
        timelineEvents={[]}
      />,
    );

    const entries = screen.getAllByTestId(/^post-investigation-entry-/);
    expect(entries.map((e) => e.dataset.testid)).toEqual([
      'post-investigation-entry-file-ticket',
      'post-investigation-entry-notify',
      'post-investigation-entry-follow-up',
    ]);

    const ticket = within(entries[0]);
    expect(ticket.getByText('TicketFiler')).toBeInTheDocument();
    expect(ticket.getByText('Completed')).toBeInTheDocument();
    expect(ticket.getByText('Acted')).toBeInTheDocument();

    expect(within(entries[1]).getByText('No action')).toBeInTheDocument();

    // Failed run: no outcome chip, no memo.
    const failed = within(entries[2]);
    expect(failed.getByText('Failed')).toBeInTheDocument();
    expect(failed.queryByText('Acted')).not.toBeInTheDocument();
    expect(failed.queryByText('No action')).not.toBeInTheDocument();
    expect(failed.getByText('No memo')).toBeInTheDocument();
  });

  it('renders the latest top-level memo as markdown with links opening in a new tab', () => {
    render(
      <PostInvestigationCard
        stages={[makeStage('s2', 'file-ticket', 2, { actions_executed: true })]}
        timelineEvents={[
          makeMemo('m-old', 's2', 'Older draft', { sequence_number: 1 }),
          makeMemo('m-new', 's2', 'Filed [TCK-1](https://tracker.example.com/TCK-1)', { sequence_number: 3 }),
          makeMemo('m-sub', 's2', 'Sub-agent answer', {
            sequence_number: 4,
            execution_id: 'exec-sub',
            parent_execution_id: 'exec-s2',
          }),
        ]}
      />,
    );

    const link = screen.getByRole('link', { name: 'TCK-1' });
    expect(link).toHaveAttribute('href', 'https://tracker.example.com/TCK-1');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(screen.queryByText('Older draft')).not.toBeInTheDocument();
    expect(screen.queryByText('Sub-agent answer')).not.toBeInTheDocument();
  });

  it('shows a running placeholder and header spinner while the stage is active', () => {
    render(
      <PostInvestigationCard
        stages={[makeStage('s2', 'file-ticket', 2, { status: EXECUTION_STATUS.ACTIVE })]}
        timelineEvents={[]}
      />,
    );
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('Running…')).toBeInTheDocument();
    expect(screen.getByRole('progressbar', { name: 'Post-investigation in progress' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();
  });

  it('starts folded when every stage finished with no action', async () => {
    const user = userEvent.setup();
    const stages = [makeStage('s2', 'file-ticket', 2, { actions_executed: false })];
    const { rerender } = render(
      <PostInvestigationCard
        stages={stages}
        timelineEvents={[makeMemo('m1', 's2', 'No issue was created.')]}
      />,
    );

    expect(screen.getByRole('button', { name: 'Expand post-investigation' })).toBeInTheDocument();
    expect(screen.getByTestId('post-investigation-header-outcome')).toHaveTextContent('No action');

    await user.click(screen.getByText('Post-investigation'));
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();
    expect(screen.queryByTestId('post-investigation-header-outcome')).not.toBeInTheDocument();
    expect(screen.getByText('No issue was created.')).toBeInTheDocument();

    rerender(
      <PostInvestigationCard
        stages={stages}
        timelineEvents={[makeMemo('m1', 's2', 'No issue was created.')]}
      />,
    );
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();
  });

  it('folds when a running stage finishes with no action, unless the user toggled', async () => {
    const user = userEvent.setup();
    const running = [makeStage('s2', 'file-ticket', 2, { status: EXECUTION_STATUS.ACTIVE })];
    const quiet = [makeStage('s2', 'file-ticket', 2, { actions_executed: false })];
    const { rerender } = render(
      <PostInvestigationCard stages={running} timelineEvents={[]} />,
    );
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();

    rerender(<PostInvestigationCard stages={quiet} timelineEvents={[]} />);
    expect(screen.getByRole('button', { name: 'Expand post-investigation' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Expand post-investigation' }));
    rerender(<PostInvestigationCard stages={running} timelineEvents={[]} />);
    rerender(<PostInvestigationCard stages={quiet} timelineEvents={[]} />);
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();
  });

  it('stays open when a finished stage has no YES/NO marker', () => {
    render(
      <PostInvestigationCard
        stages={[makeStage('s2', 'file-ticket', 2, { actions_executed: null })]}
        timelineEvents={[makeMemo('m1', 's2', 'Marker missing.')]}
      />,
    );
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();
    expect(screen.queryByTestId('post-investigation-header-outcome')).not.toBeInTheDocument();
    expect(screen.getByText('Marker missing.')).toBeInTheDocument();
  });

  it('collapses when collapseCounter changes and expands on header click', async () => {
    const user = userEvent.setup();
    const stages = [makeStage('s2', 'file-ticket', 2, { actions_executed: true })];
    const { rerender } = render(
      <PostInvestigationCard stages={stages} timelineEvents={[]} collapseCounter={0} />,
    );
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();

    rerender(<PostInvestigationCard stages={stages} timelineEvents={[]} collapseCounter={1} />);
    expect(screen.getByRole('button', { name: 'Expand post-investigation' })).toBeInTheDocument();

    await user.click(screen.getByText('Post-investigation'));
    expect(screen.getByRole('button', { name: 'Collapse post-investigation' })).toBeInTheDocument();
  });
});
