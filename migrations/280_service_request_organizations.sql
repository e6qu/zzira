-- A request was shared with whatever organizations the person who raised it
-- belonged to: the site worked it out rather than being told. JSM asks the
-- customer when they raise one -- share it with an organization, or keep it to
-- yourself -- and lets them change their mind afterwards. Sharing is now a
-- fact about the request.
CREATE TABLE service_request_organizations (
  request_issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  organization_id  TEXT NOT NULL REFERENCES service_organizations(id) ON DELETE CASCADE,
  shared_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (request_issue_id, organization_id)
);
CREATE INDEX service_request_organizations_organization
  ON service_request_organizations(organization_id, request_issue_id);

-- What the site used to work out is written down, so nothing a colleague
-- could find yesterday disappears today: every request is shared with the
-- organizations its customer belonged to that its desk serves.
INSERT INTO service_request_organizations(request_issue_id, organization_id)
SELECT sr.issue_id, dso.organization_id
FROM service_requests sr
JOIN service_desk_organizations dso ON dso.service_desk_id=sr.service_desk_id
JOIN service_organization_users sou ON sou.organization_id=dso.organization_id AND sou.user_id=sr.customer_id
ON CONFLICT DO NOTHING;
