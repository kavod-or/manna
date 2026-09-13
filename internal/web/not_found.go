package web

const notFoundPage = `<!doctype html>
<html lang="de">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="robots" content="noindex">
  <title>404 · Manna</title>
  <link rel="icon" href="/static/favicon.png?v=2" type="image/png">
  <link rel="stylesheet" href="/static/styles.css?v=30">
</head>
<body class="landing-page">
  <main class="notice-card">
    <img src="/static/logo.png?v=3" alt="Manna" width="512" height="168">
    <p class="error-code">404</p>
    <h1>Hier ist noch nicht gedeckt.</h1>
    <p>Diese Seite gibt es nicht. Scanne den QR-Code vor Ort, um das richtige Menü zu öffnen.</p>
    <div class="notice-translation" lang="en">
      <h2>This table hasn’t been set.</h2>
      <p>This page couldn’t be found. Scan the QR code at the venue to open the right menu.</p>
    </div>
  </main>
  <footer class="notice-footer">
    <span class="app-version app-version-label">Powered by Manna v{{version}}</span>
    <a class="github-link" href="https://github.com/kavod-or/manna" target="_blank" rel="noopener noreferrer" aria-label="Manna on GitHub"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 1C5.923 1 1 5.923 1 12c0 4.867 3.149 8.979 7.521 10.436.55.096.756-.233.756-.522 0-.262-.013-1.128-.013-2.049-3.059.664-3.703-1.3-3.703-1.3-.5-1.272-1.221-1.611-1.221-1.611-.998-.682.075-.668.075-.668 1.105.078 1.686 1.134 1.686 1.134.98 1.68 2.573 1.195 3.2.914.099-.71.384-1.195.698-1.47-2.442-.278-5.01-1.221-5.01-5.432 0-1.2.43-2.18 1.132-2.95-.114-.278-.49-1.396.108-2.91 0 0 .923-.295 3.025 1.127A10.5 10.5 0 0 1 12 6.82c.935.004 1.876.126 2.754.37 2.1-1.422 3.022-1.127 3.022-1.127.6 1.514.224 2.632.11 2.91.705.77 1.13 1.75 1.13 2.95 0 4.222-2.573 5.15-5.023 5.423.394.34.745 1.01.745 2.038 0 1.472-.013 2.657-.013 3.02 0 .292.2.623.763.517C19.858 20.974 23 16.866 23 12c0-6.077-4.923-11-11-11Z"/></svg></a>
  </footer>
</body>
</html>`
