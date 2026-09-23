// A SAML identity provider for the browser suite: it answers a sign-in with
// an assertion it signs, so a journey can walk the whole flow without a real
// provider. The key and certificate are generated when it starts and are
// good for nothing else.
import http from 'node:http';
import crypto from 'node:crypto';
import zlib from 'node:zlib';

const host = '127.0.0.1';
const port = 8200;
const entityID = `http://${host}:${port}/metadata`;

const { privateKey, publicKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
const certificate = makeCertificate(privateKey, publicKey);

// A SAML signing certificate: this provider signs assertions with the key
// inside it, and the site is configured with the certificate.
function makeCertificate() {
  // Node cannot write an X.509 certificate on its own, so one is assembled by
  // hand: a minimal DER certificate carrying the public key, signed with the
  // private key. Everything a SAML reader needs is the key and the signature.
  const spki = publicKey.export({ type: 'spki', format: 'der' });
  const name = der(0x30, Buffer.concat([
    der(0x31, der(0x30, Buffer.concat([
      der(0x06, Buffer.from([0x55, 0x04, 0x03])),
      der(0x0c, Buffer.from('fake saml idp', 'utf8')),
    ]))),
  ]));
  const validity = der(0x30, Buffer.concat([
    der(0x17, Buffer.from('200101000000Z', 'utf8')),
    der(0x17, Buffer.from('400101000000Z', 'utf8')),
  ]));
  const algorithm = der(0x30, Buffer.concat([
    der(0x06, Buffer.from([0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0b])),
    der(0x05, Buffer.alloc(0)),
  ]));
  const tbs = der(0x30, Buffer.concat([
    der(0xa0, der(0x02, Buffer.from([0x02]))),
    der(0x02, Buffer.from([0x01])),
    algorithm, name, validity, name, spki,
  ]));
  const signature = crypto.sign('sha256', tbs, privateKey);
  const bitString = der(0x03, Buffer.concat([Buffer.from([0x00]), signature]));
  return der(0x30, Buffer.concat([tbs, algorithm, bitString])).toString('base64');
}

function der(tag, body) {
  const length = body.length;
  let header;
  if (length < 0x80) header = Buffer.from([tag, length]);
  else if (length < 0x100) header = Buffer.from([tag, 0x81, length]);
  else header = Buffer.from([tag, 0x82, length >> 8, length & 0xff]);
  return Buffer.concat([header, body]);
}

// This provider writes its XML in canonical form already: one namespace
// declaration per element, attributes in the order canonical XML sorts them,
// no comments and no self-closing tags. So what it signs is what it wrote.
function canonical(xml) {
  return xml;
}

function signedAssertion({ acs, entity, requestID, email, name }) {
  const now = new Date();
  const instant = new Date(now.getTime() - 60_000).toISOString().replace(/\.\d+Z$/, 'Z');
  const until = new Date(now.getTime() + 5 * 60_000).toISOString().replace(/\.\d+Z$/, 'Z');
  const assertionID = 'assertion-' + crypto.randomUUID();
  // The attributes are written in the order canonical XML puts them, so what
  // is signed here is what the reader canonicalizes there.
  const assertion = `<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="${assertionID}" IssueInstant="${instant}" Version="2.0">` +
    `<saml:Issuer>${entityID}</saml:Issuer>` +
    `<saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">${email}</saml:NameID>` +
    `<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">` +
    `<saml:SubjectConfirmationData InResponseTo="${requestID}" NotOnOrAfter="${until}" Recipient="${acs}"></saml:SubjectConfirmationData>` +
    `</saml:SubjectConfirmation></saml:Subject>` +
    `<saml:Conditions NotBefore="${instant}" NotOnOrAfter="${until}">` +
    `<saml:AudienceRestriction><saml:Audience>${entity}</saml:Audience></saml:AudienceRestriction></saml:Conditions>` +
    `<saml:AuthnStatement AuthnInstant="${instant}" SessionIndex="session-${assertionID}">` +
    `<saml:AuthnContext><saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>` +
    `<saml:AttributeStatement><saml:Attribute Name="http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name">` +
    `<saml:AttributeValue>${name}</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>` +
    `</saml:Assertion>`;

  const digest = crypto.createHash('sha256').update(canonical(assertion), 'utf8').digest('base64');
  const signedInfoBody = `<ds:CanonicalizationMethod Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"></ds:CanonicalizationMethod>` +
    `<ds:SignatureMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"></ds:SignatureMethod>` +
    `<ds:Reference URI="#${assertionID}"><ds:Transforms>` +
    `<ds:Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"></ds:Transform>` +
    `<ds:Transform Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"></ds:Transform></ds:Transforms>` +
    `<ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"></ds:DigestMethod>` +
    `<ds:DigestValue>${digest}</ds:DigestValue></ds:Reference>`;
  const standalone = `<ds:SignedInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">${signedInfoBody}</ds:SignedInfo>`;
  const signature = crypto.sign('sha256', Buffer.from(canonical(standalone), 'utf8'), privateKey).toString('base64');
  const signatureBlock = `<ds:Signature xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:SignedInfo>${signedInfoBody}</ds:SignedInfo>` +
    `<ds:SignatureValue>${signature}</ds:SignatureValue></ds:Signature>`;
  const signed = assertion.replace(`</saml:Issuer>`, `</saml:Issuer>${signatureBlock}`);
  return `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ` +
    `ID="response-${crypto.randomUUID()}" Version="2.0" IssueInstant="${instant}" Destination="${acs}" InResponseTo="${requestID}">` +
    `<saml:Issuer>${entityID}</saml:Issuer>` +
    `<samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"></samlp:StatusCode></samlp:Status>` +
    signed + `</samlp:Response>`;
}

const server = http.createServer((request, response) => {
  const requestURL = new URL(request.url || '/', `http://${host}:${port}`);
  if (requestURL.pathname === '/healthz') {
    response.writeHead(204).end();
    return;
  }
  if (requestURL.pathname === '/certificate') {
    response.writeHead(200, { 'Content-Type': 'text/plain' }).end(certificate);
    return;
  }
  if (requestURL.pathname === '/metadata') {
    response.writeHead(200, { 'Content-Type': 'text/plain' }).end(entityID);
    return;
  }
  if (requestURL.pathname !== '/sso') {
    response.writeHead(404, { 'Content-Type': 'text/plain' }).end('Not found');
    return;
  }
  const encoded = requestURL.searchParams.get('SAMLRequest');
  if (!encoded) {
    response.writeHead(400, { 'Content-Type': 'text/plain' }).end('No request');
    return;
  }
  const xml = zlib.inflateRawSync(Buffer.from(encoded, 'base64')).toString('utf8');
  const requestID = /ID="([^"]+)"/.exec(xml)?.[1] ?? '';
  const acs = /AssertionConsumerServiceURL="([^"]+)"/.exec(xml)?.[1] ?? '';
  const entity = /<saml:Issuer>([^<]+)<\/saml:Issuer>/.exec(xml)?.[1] ?? '';
  const email = process.env.FAKE_SAML_EMAIL || 'saml.person@zzira.dev';
  const name = process.env.FAKE_SAML_NAME || 'SAML Person';
  const samlResponse = Buffer.from(signedAssertion({ acs, entity, requestID, email, name }), 'utf8').toString('base64');
  // The provider posts its answer back, as the HTTP-POST binding says.
  response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }).end(
    `<!doctype html><html><body onload="document.forms[0].submit()">` +
    `<form method="post" action="${acs}"><input type="hidden" name="SAMLResponse" value="${samlResponse}">` +
    `<button type="submit">Continue</button></form></body></html>`);
});

server.listen(port, host, () => {
  process.stdout.write(`fake saml idp on http://${host}:${port}\n`);
});
