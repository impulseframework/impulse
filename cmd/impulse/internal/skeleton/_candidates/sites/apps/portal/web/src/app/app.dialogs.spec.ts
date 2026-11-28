import { Methods } from '@app/service/zz_gen_constants';
import { methodMeta } from '@app/service/zz_gen_methods';
import { dialogGaps } from '@impulseframework/resource-angular/resource';
import { MethodMeta } from '@impulseframework/resource-angular/types';

// A person runs every generated method through a dialog the library draws from the
// method's metadata. This spec holds the application's generated methods to dialogs a
// person can complete: every field a dialog asks for is typed, ticked or picked from a
// list, and a failure names the method and the field.
describe('the dialogs of the generated methods', () => {
  it('leave no field a person cannot fill: each is typed, ticked or picked from a list', () => {
    const methods = Object.values(Methods).map((method) => [method, methodMeta(method) as MethodMeta] as const);
    expect(dialogGaps(methods).map((gap) => `${gap.method}.${gap.field}: ${gap.reason}`)).toEqual([]);
  });
});
