/**
 * Tab title and favicon badge for the review backlog.
 * 100 or more is 99+ on the icon and the real total in the title.
 */

export const APP_TITLE = 'TARSy';

export function tabTitle(count: number | null): string {
  if (count == null) return APP_TITLE;
  return `(${count}) ${APP_TITLE}`;
}

export function faviconBadgeText(count: number): string {
  return count >= 100 ? '99+' : String(count);
}

let originalHref: string | null = null;
let logo: HTMLImageElement | null = null;
let logoSrc: string | null = null;
let drawToken = 0;

function iconLink(): HTMLLinkElement {
  const existing = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (existing) return existing;
  const link = document.createElement('link');
  link.rel = 'icon';
  link.type = 'image/png';
  document.head.appendChild(link);
  return link;
}

function ensureOriginalHref(): string {
  if (!originalHref) {
    originalHref = iconLink().getAttribute('href') || '/tarsy-logo.png';
  }
  return originalHref;
}

function loadLogo(src: string): Promise<HTMLImageElement> {
  if (logo && logoSrc === src) return Promise.resolve(logo);
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      logo = img;
      logoSrc = src;
      resolve(img);
    };
    img.onerror = () => reject(new Error('failed to load tab icon'));
    img.src = src;
  });
}

function drawBadge(ctx: CanvasRenderingContext2D, text: string): void {
  const wide = text.length > 2;
  const radius = wide ? 12 : 9;
  const cx = 32 - radius + (wide ? 2 : 0);
  const cy = 32 - radius + 1;
  ctx.beginPath();
  ctx.arc(cx, cy, radius, 0, Math.PI * 2);
  ctx.fillStyle = '#c62828';
  ctx.fill();
  ctx.fillStyle = '#ffffff';
  ctx.font = `bold ${wide ? 8 : 12}px sans-serif`;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.fillText(text, cx, cy + 0.5);
}

export function applyTabIndicator(count: number | null): void {
  document.title = tabTitle(count);
  const link = iconLink();
  if (count == null) {
    drawToken += 1;
    if (originalHref) link.href = originalHref;
    return;
  }
  const token = ++drawToken;
  const src = ensureOriginalHref();
  void loadLogo(src).then((img) => {
    if (token !== drawToken) return;
    const canvas = document.createElement('canvas');
    canvas.width = 32;
    canvas.height = 32;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.drawImage(img, 0, 0, 32, 32);
    drawBadge(ctx, faviconBadgeText(count));
    link.href = canvas.toDataURL('image/png');
  }).catch(() => {
    // The title still carries the count when the icon cannot be drawn.
  });
}
