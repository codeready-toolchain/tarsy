import { memo, useCallback } from 'react';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import ThinkingItem from './ThinkingItem';
import ResponseItem from './ResponseItem';
import ToolCallItem from './ToolCallItem';
import ToolSummaryItem from './ToolSummaryItem';
import UserQuestionItem from './UserQuestionItem';
import NativeToolItem from './NativeToolItem';
import ErrorItem from './ErrorItem';
import ProviderFallbackItem from './ProviderFallbackItem';
import SkillLoadedItem from './SkillLoadedItem';

interface TimelineItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: (itemId: string) => void;
  expandAll?: boolean;
  expandAllToolCalls?: boolean;
  isCollapsible?: boolean;
  searchTerm?: string;
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
  searchTerm,
}: TimelineItemProps) {
  const handleToggle = useCallback(() => {
    onToggleAutoCollapse?.(item.id);
  }, [onToggleAutoCollapse, item.id]);
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
          onToggleAutoCollapse={handleToggle}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
          searchTerm={searchTerm}
        />
      );

    case FLOW_ITEM.RESPONSE:
      return (
        <ResponseItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={handleToggle}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
          searchTerm={searchTerm}
        />
      );

    case FLOW_ITEM.FINAL_ANALYSIS:
    case FLOW_ITEM.EXECUTIVE_SUMMARY:
      return (
        <ResponseItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={handleToggle}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
          searchTerm={searchTerm}
        />
      );

    case FLOW_ITEM.TOOL_CALL:
      return <ToolCallItem item={item} expandAll={expandAllToolCalls} searchTerm={searchTerm} />;

    case FLOW_ITEM.TOOL_SUMMARY:
      return (
        <ToolSummaryItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={handleToggle}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
          searchTerm={searchTerm}
        />
      );

    case FLOW_ITEM.USER_QUESTION:
      return <UserQuestionItem item={item} searchTerm={searchTerm} />;

    case FLOW_ITEM.CODE_EXECUTION:
    case FLOW_ITEM.SEARCH_RESULT:
    case FLOW_ITEM.URL_CONTEXT:
      return <NativeToolItem item={item} searchTerm={searchTerm} />;

    case FLOW_ITEM.ERROR:
      return <ErrorItem item={item} searchTerm={searchTerm} />;

    case FLOW_ITEM.PROVIDER_FALLBACK:
      return <ProviderFallbackItem item={item} searchTerm={searchTerm} />;

    case FLOW_ITEM.SKILL_LOADED:
      return (
        <SkillLoadedItem
          item={item}
          isAutoCollapsed={isAutoCollapsed}
          onToggleAutoCollapse={handleToggle}
          expandAll={expandAll}
          isCollapsible={isCollapsible}
          searchTerm={searchTerm}
        />
      );

    case FLOW_ITEM.STAGE_SEPARATOR:
      // Stage separators are handled by the ConversationTimeline container
      return null;

    default:
      return null;
  }
}

export default memo(TimelineItem);
