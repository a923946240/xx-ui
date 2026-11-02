import { act, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import PanelAtmosphere from '@/layouts/PanelAtmosphere';

vi.mock('@/hooks/useTheme', () => ({ useTheme: () => ({ isDark: true, isUltra: false }) }));

describe('panel atmosphere lifecycle', () => {
  let reduced = false;
  let hidden = false;
  let motionChange: () => void;
  const cancel = vi.fn();
  const request = vi.fn(() => 17);

  beforeEach(() => {
    reduced = false;
    hidden = false;
    cancel.mockClear();
    request.mockClear();
    vi.stubGlobal('requestAnimationFrame', request);
    vi.stubGlobal('cancelAnimationFrame', cancel);
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    vi.spyOn(window, 'matchMedia').mockImplementation(
      () =>
        ({
          get matches() {
            return reduced;
          },
          addEventListener: (_: string, listener: () => void) => {
            motionChange = listener;
          },
          removeEventListener: vi.fn(),
        }) as unknown as MediaQueryList,
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      setTransform: vi.fn(),
      clearRect: vi.fn(),
      beginPath: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
    } as unknown as CanvasRenderingContext2D);
  });

  it('cancels frames while hidden and when unmounted', () => {
    const page = render(<PanelAtmosphere />);
    expect(request).toHaveBeenCalledTimes(1);
    act(() => {
      hidden = true;
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(cancel).toHaveBeenCalledWith(17);
    const pausedCount = request.mock.calls.length;
    act(() => {
      hidden = false;
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(request.mock.calls.length).toBe(pausedCount + 1);
    page.unmount();
    expect(cancel).toHaveBeenCalledTimes(2);
  });

  it('does not animate under reduced motion and responds to preference changes', () => {
    reduced = true;
    const page = render(<PanelAtmosphere />);
    expect(request).not.toHaveBeenCalled();
    act(() => {
      reduced = false;
      motionChange();
    });
    expect(request).toHaveBeenCalledTimes(1);
    act(() => {
      reduced = true;
      motionChange();
    });
    expect(cancel).toHaveBeenCalledWith(17);
    page.unmount();
  });
});
