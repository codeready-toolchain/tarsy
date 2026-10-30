/**
 * Page Notification permission. Loading the app never calls requestPermission.
 */

import type { BrowserNotificationPermission, ToastCandidate } from './notificationDecision.ts';

export type { BrowserNotificationPermission };

export function notificationPermission(): BrowserNotificationPermission {
  if (typeof Notification === 'undefined') return 'unsupported';
  return Notification.permission;
}

export function requestNotificationPermission(): Promise<BrowserNotificationPermission> {
  if (typeof Notification === 'undefined' || typeof Notification.requestPermission !== 'function') {
    return Promise.resolve('unsupported');
  }
  if (Notification.permission !== 'default') return Promise.resolve(Notification.permission);
  return Notification.requestPermission()
    .then((permission) => permission)
    .catch(() => notificationPermission());
}

export function showSessionNotification(
  candidate: ToastCandidate,
  onOpen: (sessionId: string) => void,
): void {
  if (typeof Notification === 'undefined') return;
  try {
    const notification = new Notification(candidate.title, {
      body: candidate.body,
      tag: candidate.id,
    });
    notification.onclick = () => {
      onOpen(candidate.id);
      notification.close();
    };
  } catch {
    // The counter still works when the browser refuses a page notification.
  }
}
