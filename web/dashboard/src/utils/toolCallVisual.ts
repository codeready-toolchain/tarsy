import type { ComponentType } from 'react';
import {
  AutoStoriesOutlined,
  CheckCircle,
  Error as ErrorIcon,
  InfoOutlined,
} from '@mui/icons-material';
import type { SvgIconProps } from '@mui/material/SvgIcon';
import { TOOL_TYPE } from '../constants/toolTypes';

/** MUI palette keys used for tool-call borders / accents in timeline and streaming UI. */
export type ToolVisualAccentKey = 'error' | 'warning' | 'info' | 'primary' | 'success';

export type ToolCallStatusIcon = ComponentType<SvgIconProps>;

export type GetToolVisualConfigOptions =
  | { mode: 'streaming' }
  | {
      mode: 'completed';
      isMcpFailure: boolean;
      isToolResultError: boolean;
    };

export interface ToolVisualStreamingConfig {
  accentKey: ToolVisualAccentKey;
  StatusIcon?: undefined;
}

export interface ToolVisualCompletedConfig {
  accentKey: ToolVisualAccentKey;
  StatusIcon: ToolCallStatusIcon;
}

/**
 * Central mapping from tool type + execution state to palette accent and optional
 * status icon. Used by ToolCallItem (completed) and StreamingContentRenderer
 * (in-progress).
 */
export function getToolVisualConfig(
  toolType: string | undefined,
  options: { mode: 'streaming' },
): ToolVisualStreamingConfig;
export function getToolVisualConfig(
  toolType: string | undefined,
  options: {
    mode: 'completed';
    isMcpFailure: boolean;
    isToolResultError: boolean;
  },
): ToolVisualCompletedConfig;
export function getToolVisualConfig(
  toolType: string | undefined,
  options: GetToolVisualConfigOptions,
): ToolVisualStreamingConfig | ToolVisualCompletedConfig {
  if (options.mode === 'streaming') {
    const isMemory = toolType === TOOL_TYPE.MEMORY;
    const isSkill = toolType === TOOL_TYPE.SKILL;
    const isGoogleNative = toolType === TOOL_TYPE.GOOGLE_NATIVE;
    if (isMemory) return { accentKey: 'success' };
    if (isSkill || isGoogleNative) return { accentKey: 'info' };
    return { accentKey: 'primary' };
  }

  const { isMcpFailure, isToolResultError } = options;
  if (isMcpFailure) {
    return { accentKey: 'error', StatusIcon: ErrorIcon };
  }
  if (isToolResultError) {
    return { accentKey: 'warning', StatusIcon: InfoOutlined };
  }
  if (toolType === TOOL_TYPE.SKILL) {
    return { accentKey: 'info', StatusIcon: AutoStoriesOutlined };
  }
  if (toolType === TOOL_TYPE.GOOGLE_NATIVE) {
    return { accentKey: 'info', StatusIcon: InfoOutlined };
  }
  return { accentKey: 'primary', StatusIcon: CheckCircle };
}

