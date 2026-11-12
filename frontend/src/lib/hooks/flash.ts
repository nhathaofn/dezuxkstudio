/**
 * A value that resets itself after a delay ("Copied!", "click again to confirm"...).
 * Re-flashing restarts the delay; dispose() cancels a pending reset (component unmount).
 * Kept framework-free so it is unit-testable; useFlash wraps it for React.
 */
export class Flash<T> {
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    private readonly ms: number,
    private readonly onChange: (value: T | null) => void
  ) {}

  show(value: T): void {
    this.cancel();
    this.onChange(value);
    this.timer = setTimeout(() => {
      this.timer = null;
      this.onChange(null);
    }, this.ms);
  }

  clear(): void {
    this.cancel();
    this.onChange(null);
  }

  dispose(): void {
    this.cancel();
  }

  private cancel(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }
}
