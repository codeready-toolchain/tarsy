import { memo } from 'react';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
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
  expandAllToolCalls?: boolean;
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
  expandAllToolCalls = false,
  isCollapsible = false,
}: TimelineItemProps) {
  // Hide response/executive_summary items with empty content. Defense-in-depth
  // for truncated WS payloads that may slip through the truncation handler.
  if ((!item.content || !item.content.trim()) && (item.type === FLOW_ITEM.RESPONSE || item.type === FLOW_ITEM.EXECUTIVE_SUMMARY)) {
    return null;
  }

  switch (item.type) {
    case FLOW_ITEM.THINKING:
      return (
        <ThinkingItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case FLOW_ITEM.RESPONSE:
      return (
        <ResponseItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case FLOW_ITEM.FINAL_ANALYSIS:
    case FLOW_ITEM.EXECUTIVE_SUMMARY:
      return (
        <ResponseItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case FLOW_ITEM.TOOL_CALL:
      return <ToolCallItem item={item} expandAll={expandAllToolCalls} />;

    case FLOW_ITEM.TOOL_SUMMARY:
      return (
        <ToolSummaryItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={onToggleAutoCollapse}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
        />
      );

    case FLOW_ITEM.USER_QUESTION:
      return <UserQuestionItem item={item} />;

    case FLOW_ITEM.CODE_EXECUTION:
    case FLOW_ITEM.SEARCH_RESULT:
    case FLOW_ITEM.URL_CONTEXT:
      return <NativeToolItem item={item} />;

    case FLOW_ITEM.ERROR:
      return <ErrorItem item={item} />;

    case FLOW_ITEM.STAGE_SEPARATOR:
      // Stage separators are handled by the ConversationTimeline container
      return null;

    default:
      return null;
  }
}

export default memo(TimelineItem);
