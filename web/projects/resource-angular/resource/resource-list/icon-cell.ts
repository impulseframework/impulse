import { ColumnIconConfig, DataType, RecordData } from '@impulseframework/resource-angular/types';

/** What an icon column shows for one row: the icon, or none when the value maps to none, and the words. */
export interface IconCell {
  icon: string | null;
  words: string;
}

/** Formats one of the row's fields for a sentence: the list hands the field's display-type formatter. */
export type FieldText = (field: string, value: DataType | null) => string;

/** A placeholder in a tooltip sentence: the row's field in braces, `{activeLearners}`. */
const placeholder = /\{([A-Za-z_][A-Za-z0-9_]*)\}/g;

/**
 * The row fields an icon column's sentences name in braces, so the list requests them
 * beside its columns; a sentence naming no field adds none.
 */
export function iconTemplateFields(config: ColumnIconConfig): string[] {
  const sentences = [...Object.values(config.tooltips ?? {}), ...(config.empty?.tooltip ? [config.empty.tooltip] : [])];
  const fields = new Set<string>();
  for (const sentence of sentences) {
    for (const match of sentence.matchAll(placeholder)) {
      fields.add(match[1]);
    }
  }
  return [...fields];
}

/** Fills a sentence's placeholders from the row: each field by its formatted text, an absent field by nothing. */
export function fillSentence(sentence: string, row: RecordData, fieldText: FieldText): string {
  return sentence.replace(placeholder, (_, field: string) => (field in row ? fieldText(field, row[field]) : ''));
}

/**
 * Resolves an icon column's cell from the raw row and the cell's formatted text. A null
 * or absent value takes the `empty` entry, else no icon and the formatted text (the
 * column's empty text). Any other value is keyed by its text: the icon from `icons`, the
 * words from `tooltips` with its placeholders filled from the row, else the formatted
 * text; a value with no icon keeps the text, so declaring icons for some values and not
 * others degrades to text.
 */
export function resolveIconCell(
  config: ColumnIconConfig,
  row: RecordData,
  field: string,
  text: string,
  fieldText: FieldText,
): IconCell {
  const raw = row[field];
  if (raw === null || raw === undefined || raw === '') {
    if (!config.empty) {
      return { icon: null, words: text };
    }
    return { icon: config.empty.icon, words: config.empty.tooltip ? fillSentence(config.empty.tooltip, row, fieldText) : text };
  }
  const key = String(raw);
  const icon = Object.hasOwn(config.icons, key) ? config.icons[key] : null;
  const sentence = config.tooltips && Object.hasOwn(config.tooltips, key) ? config.tooltips[key] : undefined;
  return { icon, words: sentence === undefined ? text : fillSentence(sentence, row, fieldText) };
}
