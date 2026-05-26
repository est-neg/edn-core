# Social Media Growth Context

## Scope

This context supports social channel strategy and Meta Business operations for company products, starting with Funcionario.online.

## First Product: Funcionario.online

- Primary site: `https://developer.funcionario.online`
- Positioning: `O funcionario que nunca falta. O processo que nunca improvisa.`
- Core promise: increase atendimento capacity, consistency, organization, and continuity without expanding headcount at the same rate.
- Primary segments in the current site copy:
  - Secretaria Administrativa for general companies and operations
  - Secretaria Clinica Medica for clinics and consultorios
  - Secretaria Clinica Odontologica for dental clinics
- Operating themes that should remain consistent in social content:
  - atendimento 24h
  - padrao profissional
  - menos sobrecarga da equipe
  - agenda e fluxo mais organizados
  - privacidade e comunicacao profissional, especially for healthcare contexts

## Known Channels

- Instagram: `@funcionario.online`
- Facebook: `https://www.facebook.com/funcionario.online.ia` (corrected and confirmed)
- WhatsApp: the public site already routes prospects through a WhatsApp CTA for commercial guidance and proposal intake.
- Production site: `https://www.funcionario.online` (currently in development)
- Development site: `https://developer.funcionario.online` (active and publicly reachable)

## Initial Growth Objectives

- Build organic demand without paid media in the first phase.
- Convert social attention into site visits, WhatsApp conversations, and proposal requests.
- Establish a repeatable posting system for feed posts, reels, stories, and cross-channel repurposing.
- Capture insight data per channel to support A/B testing and decision making.
- Keep brand messaging consistent with operational reliability, professionalism, and business outcomes.

## Initial Operational Questions

- Which Meta Business Manager owns the Facebook page, Instagram account, and WhatsApp assets?
- Which site events should be the first conversion signals: WhatsApp click, proposal form submission, plan view, or profile selection?
- What data store should retain raw post metrics, experiment variants, and attributed conversion outcomes?

## Meta Business API Access Requirements

See the access checklist in the session work log. Required before automation or analytics work:
- Meta Business Manager ID that owns `funcionario.online.ia`
- App ID and App Secret for a Meta Business app with `pages_manage_posts`, `pages_read_engagement`, `instagram_basic`, and `instagram_content_publish` permissions
- Page Access Token with long-lived scope for the Facebook page
- Instagram Business Account ID linked to the Facebook page
- WhatsApp Business Account ID and Phone Number ID if messaging automation is in scope
