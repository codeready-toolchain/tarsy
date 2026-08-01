import '@testing-library/jest-dom';
import { vi } from 'vitest';

// jsdom doesn't implement matchMedia — MUI's useMediaQuery (used for responsive
// layout like the mobile nav drawer) needs a stub or every render throws.
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});
