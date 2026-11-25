// Package webui embarque le front (HTML, JS, CSS, uPlot) dans le binaire : aucune ressource
// externe au runtime (§0 du plan).
package webui

import "embed"

// FS contient index.html, app.js, style.css et vendor/ (uPlot 1.6.32).
//
//go:embed index.html app.js i18n.js i18n.tsv style.css favicon.svg vendor/uPlot.iife.min.js vendor/uPlot.min.css
var FS embed.FS

// Catalog : traductions de la console et de l'interface web (une ligne par texte source français,
// colonnes fr en es it pl de).
//
//go:embed i18n.tsv
var Catalog string
