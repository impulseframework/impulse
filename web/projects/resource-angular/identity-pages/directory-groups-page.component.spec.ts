import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute } from '@angular/router';
import { notNamedFor } from '../auth-frame/frame-testing';
import { DirectoryGroupsPageComponent } from './directory-groups-page.component';
import { directoryGroupsSaysKey, DirectoryGroupsSays } from './directory-groups-says';

/** What a route of the page carries: the heading and the sentences. */
const says: DirectoryGroupsSays = {
  title: 'Groups',
  paragraphs: ['Your directory decides who holds each role here.', 'There are no groups to keep in this application.'],
};

describe('DirectoryGroupsPageComponent', () => {
  let fixture: ComponentFixture<DirectoryGroupsPageComponent>;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;

  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [DirectoryGroupsPageComponent],
      providers: [{ provide: ActivatedRoute, useValue: { snapshot: { data: { [directoryGroupsSaysKey]: says } } } }],
    });
    fixture = TestBed.createComponent(DirectoryGroupsPageComponent);
    fixture.detectChanges();
  });

  it("says the route's heading and sentences, a paragraph each", () => {
    expect(element().querySelector('h1')?.textContent?.trim()).toBe('Groups');
    expect(Array.from(element().querySelectorAll('p')).map((paragraph) => paragraph.textContent?.trim())).toEqual(
      says.paragraphs,
    );
  });

  it("names its one element for the page: an application's rule for its own .directory-groups lands on none", () => {
    expect(notNamedFor('impulse-directory-groups-page-', element())).toEqual([]);
    expect(element().querySelector('section.impulse-directory-groups-page-content')).not.toBeNull();
  });
});
