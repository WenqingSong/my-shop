type ColorMode = "light" | "dark" | "system";
interface FrameworkMotionPlayer {
  readonly phase: string;
  readonly currentTime: number;
  finish(options?: { surface?: HTMLElement }): Promise<void>;
  cancel(): void;
}
interface Window {
  FrameworkTheme?: {
    readonly mode: ColorMode;
    readonly dark: boolean;
    set(mode: ColorMode): void;
  };
  FrameworkMotion?: {
    readonly version: number;
    create(
      svg: SVGSVGElement,
      options?: { loop?: boolean; speed?: number }
    ): FrameworkMotionPlayer;
  };
  FrameworkBoot?: { finish(): Promise<void>; fail(): void };
}
