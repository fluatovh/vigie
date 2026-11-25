#!/bin/sh
# Hook udhcpc (post-bound / post-renew) : l'IP vient de changer, on rafraîchit la console.
exec /usr/lib/vigie/update-issue
