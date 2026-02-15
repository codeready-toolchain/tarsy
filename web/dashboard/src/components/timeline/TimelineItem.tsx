import { memo } from 'react';
import type { FlowItem } from '../../utils/timelineParser';
import { isReActResponse } from '../../utils/timelineParser';
import ThinkingItem from './ThinkingItem';
import ResponseItem from './ResponseItem';
import ToolCallItem from './ToolCallItem';
import ToolSummaryItem from './ToolSummaryItem';
import UserQuestionItem from './UserQuestionItem';
import NativeToolItem from './NativeToolItem';
import ErrorItem from './ErrorItem';

interface TimelineItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: () => void;
  expandAll?: boolean;
  isCollapsible?: boolean;
}

/**
 * TimelineItem - router component that dispatches to the appropriate renderer
 * based on FlowItem.type.
 */
function TimelineItem({
  item,
  isAutoCollapsed = false,
  onToggleAutoCollapse,
  expandAll = false,
  isCollapsible = false,
}: TimelineItemProps) {
  switch (item.type) {
    case 'thinking':
      return (
        <ThinkingItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case 'response':
      // Hide raw ReAct-formatted llm_response events — the backend creates
      // properly-typed llm_thinking and final_analysis events for each section.
      if (isReActResponse(item.content)) return null;
      return (
        <ResponseItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case 'final_analysis':
    case 'executive_summary':
      return (
        <ResponseItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case 'tool_call':
      return <ToolCallItem item={item} />;

    case 'tool_summary':
      return (
        <ToolSummaryItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case 'user_question':
      return <UserQuestionItem item={item} />;

    case 'code_execution':
    case 'search_result':
    case 'url_context':
      return <NativeToolItem item={item} />;

    case 'error':
      return <ErrorItem item={item} />;

    case 'stage_separator':
      // Stage separators are handled by the ConversationTimeline container
      return null;

    default:
      return null;
  }
}

export default memo(TimelineItem);
