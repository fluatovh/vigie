# Environnement de QA de l'interface : Node + Chromium d'Alpine + puppeteer-core figé.
FROM node:22-alpine3.24
RUN apk add --no-cache chromium font-noto poppler-utils
WORKDIR /qa
COPY package.json ./
RUN npm install --no-audit --no-fund --omit=dev
COPY smoke.mjs report.mjs i18n.mjs ci.mjs docs.mjs ./
ENTRYPOINT ["node", "smoke.mjs"]
