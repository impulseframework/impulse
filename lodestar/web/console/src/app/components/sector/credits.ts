/**
 * An amount of credits as a page writes it: the decimal's own text with the thousands of
 * its whole part set apart, then "cr". The text is never made a number, so an amount keeps
 * every digit the server holds: "950.50 cr", "12,345,678,901,234,567.25 cr". A text that
 * is no decimal is written as it stands, and no amount writes nothing.
 */
export function credits(amount: string | null | undefined): string {
  const text = amount?.trim() ?? '';
  if (text === '') {
    return '';
  }
  const match = /^(-?)(\d+)(\.\d+)?$/.exec(text);
  if (!match) {
    return `${text} cr`;
  }
  const sign = match[1] ?? '';
  const whole = match[2] ?? '';
  const fraction = match[3] ?? '';
  return `${sign}${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${fraction} cr`;
}
