/**
 * Rehype plugin that highlights search term matches in HAST text nodes
 * by wrapping them in <mark> elements. Works with ReactMarkdown's skipHtml
 * since it operates at the AST level, not raw HTML injection.
 */

interface HastTextNode {
  type: 'text';
  value: string;
}

interface HastElementNode {
  type: 'element';
  tagName: string;
  properties: Record<string, unknown>;
  children: HastNode[];
}

type HastNode = HastTextNode | HastElementNode | { type: string; children?: HastNode[] };

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function walk(node: HastNode, regex: RegExp): void {
  if (!('children' in node) || !node.children) return;

  const newChildren: HastNode[] = [];
  for (const child of node.children) {
    if (child.type === 'text') {
      const text = (child as HastTextNode).value;
      const parts = text.split(regex);
      if (parts.length <= 1) {
        newChildren.push(child);
        continue;
      }
      for (let i = 0; i < parts.length; i++) {
        if (!parts[i]) continue;
        if (i % 2 === 1) {
          newChildren.push({
            type: 'element',
            tagName: 'mark',
            properties: { className: ['search-highlight'] },
            children: [{ type: 'text', value: parts[i] }],
          });
        } else {
          newChildren.push({ type: 'text', value: parts[i] });
        }
      }
    } else {
      walk(child, regex);
      newChildren.push(child);
    }
  }
  node.children = newChildren;
}

export function rehypeSearchHighlight(searchTerm: string) {
  if (!searchTerm.trim()) return undefined;
  const escaped = escapeRegExp(searchTerm);
  const regex = new RegExp(`(${escaped})`, 'gi');

  return () => (tree: HastNode) => {
    walk(tree, regex);
  };
}
