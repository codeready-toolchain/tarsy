import { postInvestigationTooltip } from '../../components/dashboard/postInvestigationTooltip';

const sessionId = 'sess-1';

describe('postInvestigationTooltip', () => {
  it('is the status sentence when there are no links', () => {
    expect(postInvestigationTooltip({
      id: sessionId,
      post_investigation_acted: false,
    })).toEqual({
      status: 'Post-investigation agents ran — no action taken',
      links: [],
    });
  });

  it('lists artifact urls and omits this session link', () => {
    expect(postInvestigationTooltip({
      id: sessionId,
      post_investigation_acted: true,
      post_investigation_links: [
        'https://tickets.example/SANDBOX-1',
        'https://tarsy.example/sessions/sess-1',
        'https://tarsy.example/sessions/sess-1/',
        'https://tarsy.example/app/sessions/sess-1',
        'https://notes.example/n',
        'https://tickets.example/browse?session=sess-1',
      ],
    })).toEqual({
      status: 'Post-investigation agent acted',
      links: [
        'https://tickets.example/SANDBOX-1',
        'https://notes.example/n',
        'https://tickets.example/browse?session=sess-1',
      ],
    });
  });

  it('stays the status sentence when every link is this session', () => {
    expect(postInvestigationTooltip({
      id: sessionId,
      post_investigation_acted: true,
      post_investigation_links: ['https://tarsy.example/sessions/sess-1'],
    })).toEqual({
      status: 'Post-investigation agent acted',
      links: [],
    });
  });
});
