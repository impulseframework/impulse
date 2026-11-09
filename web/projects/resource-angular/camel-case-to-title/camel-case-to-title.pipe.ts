import { Pipe, PipeTransform } from '@angular/core';

@Pipe({
  name: 'camelCaseToTitle',
})
export class CamelCaseToTitlePipe implements PipeTransform {
  transform(value: string): string {
    if (!value) {
      return value;
    }

    if (value === value.toUpperCase() && !/[0-9]/.test(value)) {
      return value;
    }

    let transformed = value.replace(/([A-Z])/g, ' $1');
    transformed = transformed.replace(/([a-zA-Z])([0-9])/g, '$1 $2');
    transformed = transformed.trim();
    transformed = transformed.charAt(0).toUpperCase() + transformed.slice(1);

    return transformed;
  }
}
