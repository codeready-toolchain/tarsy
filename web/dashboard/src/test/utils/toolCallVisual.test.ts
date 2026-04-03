/**
 * Tests for toolCallVisual.ts — accent and status icon mapping for tool calls.
 */

import {
  AutoStoriesOutlined,
  CheckCircle,
  Error as ErrorIcon,
  InfoOutlined,
} from '@mui/icons-material';
import { TOOL_TYPE } from '../../constants/toolTypes';
import { getToolVisualConfig } from '../../utils/toolCallVisual';

describe('getToolVisualConfig', () => {
  describe('mode streaming', () => {
    it.each([
      [TOOL_TYPE.MEMORY, 'success'],
      [TOOL_TYPE.SKILL, 'info'],
      [TOOL_TYPE.GOOGLE_NATIVE, 'info'],
      [TOOL_TYPE.MCP, 'primary'],
      [TOOL_TYPE.ORCHESTRATOR, 'primary'],
      [undefined, 'primary'],
    ] as const)('toolType %s -> accentKey %s', (toolType, expectedAccent) => {
      const cfg = getToolVisualConfig(toolType, { mode: 'streaming' });
      expect(cfg.accentKey).toBe(expectedAccent);
      expect(cfg).not.toHaveProperty('StatusIcon');
    });
  });

  describe('mode completed', () => {
    it('prefers MCP failure over tool type and tool result error', () => {
      const r = getToolVisualConfig(TOOL_TYPE.SKILL, {
        mode: 'completed',
        isMcpFailure: true,
        isToolResultError: true,
      });
      expect(r.accentKey).toBe('error');
      expect(r.StatusIcon).toBe(ErrorIcon);
    });

    it('tool result error when not MCP failure', () => {
      const r = getToolVisualConfig(TOOL_TYPE.MCP, {
        mode: 'completed',
        isMcpFailure: false,
        isToolResultError: true,
      });
      expect(r.accentKey).toBe('warning');
      expect(r.StatusIcon).toBe(InfoOutlined);
    });

    it('skill success', () => {
      const r = getToolVisualConfig(TOOL_TYPE.SKILL, {
        mode: 'completed',
        isMcpFailure: false,
        isToolResultError: false,
      });
      expect(r.accentKey).toBe('info');
      expect(r.StatusIcon).toBe(AutoStoriesOutlined);
    });

    it('google native success', () => {
      const r = getToolVisualConfig(TOOL_TYPE.GOOGLE_NATIVE, {
        mode: 'completed',
        isMcpFailure: false,
        isToolResultError: false,
      });
      expect(r.accentKey).toBe('info');
      expect(r.StatusIcon).toBe(InfoOutlined);
    });

    it('default MCP / orchestrator success', () => {
      const r = getToolVisualConfig(TOOL_TYPE.MCP, {
        mode: 'completed',
        isMcpFailure: false,
        isToolResultError: false,
      });
      expect(r.accentKey).toBe('primary');
      expect(r.StatusIcon).toBe(CheckCircle);
    });
  });
});
