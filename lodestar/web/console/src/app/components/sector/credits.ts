/**
 * An amount of credits as a page writes it: the decimal's own text with the thousands of
 * its whole part set apart, then "cr". The text is never made a number, so an amount keeps
 * every digit the server holds: "950.50 cr", "12,345,678,901,234,567.25 cr". An amount
 * with a fraction is written to two places at least, as money is: the server hands a
 * decimal without its closing zeros, so 950.50 arrives as "950.5" and reads "950.50 cr".
 * A whole amount shows no places ("12 cr"), and every place past the second is kept as it
 * was handed. A text that is no decimal is written as it stands, and no amount writes
 * nothing.
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
  // The point and one digit: the closing zero the server left off is written back.
  const places = fraction.length === 2 ? `${fraction}0` : fraction;
  return `${sign}${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${places} cr`;
}
