/** The route data key the sentences of the Groups page of an Auth whose roles a directory decides are carried under. */
export const directoryGroupsSaysKey = 'impulseDirectoryGroups';

/**
 * What the Groups page says where a directory decides an Auth's roles: the sentences a
 * person reads in place of a list of groups.
 */
export interface DirectoryGroupsSays {
  /** What the page is headed by. */
  title: string;
  /** The sentences, a paragraph each. */
  paragraphs: string[];
}
