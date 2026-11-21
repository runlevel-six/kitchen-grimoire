// SPDX-License-Identifier: Apache-2.0
//
// pdf2txt.js: extract the text layer of a PDF with macOS PDFKit (no extra
// installs). Used to prepare legacy documents for grimoire-import.
//
//   osascript -l JavaScript scripts/pdf2txt.js /abs/path/in.pdf /abs/path/out.txt
//
// Each page starts with a form feed and a "[[PAGE n]]" marker line. PDFKit
// collapses runs of spaces, so field separators on title lines are lost; the
// converter recovers fields from the mapping file's tokens (docs/DESIGN.md §8.4).
// On Linux, poppler's `pdftotext -layout` is an alternative (it keeps spacing).
ObjC.import('PDFKit');
function run(argv){
  const doc=$.PDFDocument.alloc.initWithURL($.NSURL.fileURLWithPath(argv[0]));
  const n=doc.pageCount; let out=[];
  for(let i=0;i<n;i++){ out.push('\f[[PAGE '+(i+1)+']]\n'+ObjC.unwrap(doc.pageAtIndex(i).string)); }
  $(out.join('\n')).writeToFileAtomicallyEncodingError(argv[1],true,$.NSUTF8StringEncoding,null);
  return n;
}
