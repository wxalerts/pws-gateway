import { qrPath } from '../lib/qr';

interface QrCodeProps {
  /** The text to encode, normally a web address. */
  value: string;
  /** Accessible description, for example "Link to the claim page". */
  label: string;
  /** Pixels per module is chosen by CSS; this is the rendered width. */
  size?: number;
}

/** A QR code drawn as inline SVG, so it needs no image source and no network. */
export function QrCode({ value, label, size = 192 }: QrCodeProps) {
  const { path, modules } = qrPath(value);
  const quiet = 4; // modules of white around the code, as the spec asks
  const total = modules + quiet * 2;
  return (
    <svg
      className="qr"
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`${-quiet} ${-quiet} ${total} ${total}`}
      shapeRendering="crispEdges"
    >
      <rect x={-quiet} y={-quiet} width={total} height={total} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  );
}
