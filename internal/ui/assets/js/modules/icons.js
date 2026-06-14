/**
 * Icon helper for the inline SVG sprite defined in index.html.
 */

export function icon(name, sizeClass = '') {
  const cls = sizeClass ? `icon ${sizeClass}` : 'icon';
  return `<svg class="${cls}" aria-hidden="true" focusable="false"><use href="#i-${name}"></use></svg>`;
}

export function setIcon(svgElement, name) {
  const use = svgElement?.querySelector('use');
  if (use) {
    use.setAttribute('href', `#i-${name}`);
  }
}
