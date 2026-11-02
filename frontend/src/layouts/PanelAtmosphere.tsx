import { useEffect, useRef } from 'react';

import { useTheme } from '@/hooks/useTheme';

export default function PanelAtmosphere() {
  const { isDark, isUltra } = useTheme();
  const canvas = useRef<HTMLCanvasElement>(null);
  const active = isDark && !isUltra;

  useEffect(() => {
    const element = canvas.current;
    if (!active || !element) return;
    const context = element.getContext('2d');
    if (!context) return;
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    const particles = Array.from({ length: 36 }, () => ({
      x: Math.random(),
      y: Math.random(),
      alpha: 0.14 + Math.random() * 0.2,
    }));
    let width = 0;
    let height = 0;
    let frame: number | null = null;
    let previous = 0;

    const draw = (time: number) => {
      const delta = previous ? Math.min(time - previous, 100) : 0;
      previous = time;
      context.clearRect(0, 0, width, height);
      for (const particle of particles) {
        particle.y = (particle.y - delta * 0.000002 + 1) % 1;
        context.fillStyle = `rgba(147,184,207,${particle.alpha})`;
        context.beginPath();
        context.arc(particle.x * width, particle.y * height, 0.8, 0, Math.PI * 2);
        context.fill();
      }
    };
    const tick = (time: number) => {
      if (time - previous >= 1000 / 24) draw(time);
      frame = requestAnimationFrame(tick);
    };
    const stop = () => {
      if (frame !== null) cancelAnimationFrame(frame);
      frame = null;
      previous = 0;
    };
    const sync = () => {
      stop();
      if (motion.matches || document.hidden) {
        context.clearRect(0, 0, width, height);
      } else {
        draw(0);
        frame = requestAnimationFrame(tick);
      }
    };
    const resize = () => {
      width = window.innerWidth;
      height = window.innerHeight;
      const ratio = Math.min(window.devicePixelRatio || 1, 1.5);
      element.width = Math.round(width * ratio);
      element.height = Math.round(height * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
      sync();
    };
    resize();
    window.addEventListener('resize', resize);
    document.addEventListener('visibilitychange', sync);
    motion.addEventListener('change', sync);
    return () => {
      stop();
      window.removeEventListener('resize', resize);
      document.removeEventListener('visibilitychange', sync);
      motion.removeEventListener('change', sync);
      context.clearRect(0, 0, width, height);
    };
  }, [active]);

  return active ? (
    <div className="panel-atmosphere" aria-hidden="true">
      <canvas ref={canvas} />
    </div>
  ) : null;
}
