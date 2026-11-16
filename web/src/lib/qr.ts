import qrcode from 'qrcode-generator';

/**
 * Encode `value` as a QR code and return its dark modules as one SVG path.
 * Exported for tests. Error correction M is plenty for a short address.
 */
export function qrPath(value: string): { path: string; modules: number } {
  const qr = qrcode(0, 'M');
  qr.addData(value);
  qr.make();
  const n = qr.getModuleCount();
  let path = '';
  for (let r = 0; r < n; r++) {
    for (let c = 0; c < n; c++) {
      if (qr.isDark(r, c)) path += `M${c} ${r}h1v1h-1z`;
    }
  }
  return { path, modules: n };
}
