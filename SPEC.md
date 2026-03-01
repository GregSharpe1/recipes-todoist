Project Specification: Go Recipe Dashboard & Todoist Automator
1. Project Overview

Develop a standalone Go web application that serves as a local recipe dashboard. Users can create recipes (including uploading a photo and listing ingredients), which are saved to PostgreSQL. Users can trigger adding a recipe's ingredients to Todoist via two methods:

    Clicking a button directly on the frontend UI.

    Scanning a dynamically generated QR code linked to that recipe.

2. Tech Stack

    Backend: Go 1.26 using standard net/http (taking advantage of the latest ServeMux routing improvements introduced in recent Go versions).

    Frontend: Go html/template (HTML/CSS).

    Data Storage: PostgreSQL.

    File Storage: Local file system for uploaded recipe images.

    External API: Todoist REST API v2.

    QR Generation: github.com/skip2/go-qrcode.

3. File Structure
Plaintext

/
├── main.go               # Server setup, static file serving, routing
├── handlers.go           # HTTP handlers (UI, form parsing, Todoist logic)
├── (PostgreSQL)          # Persistent storage for recipes
├── (no .env file)        # Runtime config is read from process environment variables
├── templates/
│   └── index.html        # Main dashboard and submission form
└── uploads/              # Directory to save uploaded recipe photos

4. Application Routes
Method	Endpoint	Description
GET	/	Renders UI dashboard with recipe cards (photos, names) and the creation form.
GET	/uploads/{file}	Serves static image files for the frontend to display.
POST	/api/recipes	Receives multipart/form-data, saves the image to /uploads/, and inserts recipe data into PostgreSQL.
POST	/api/push/{id}	UI-triggered endpoint. Reads recipe {id} from PostgreSQL, pushes ingredients to Todoist.
GET	/scan/{id}	QR-triggered webhook. Pushes to Todoist and returns a mobile-friendly success page.
GET	/qr/{id}	Generates and serves a PNG of the QR code for a specific recipe.
5. Functional Requirements
A. Data Management (PostgreSQL)

The app must safely read/write recipes in PostgreSQL. A `recipes` table should include `id`, `name`, `image_path`, and ingredients stored as JSON.

B. Image Upload Handling

    The "Add Recipe" HTML form must use enctype="multipart/form-data".

    The Go backend must parse the incoming file, generate a unique filename (e.g., using time.Now().UnixNano() to prevent overwrites), save it to the /uploads/ directory, and store that relative path in PostgreSQL.

C. Frontend UI (index.html)

    Recipe Creation Form: Inputs for Name, a File Upload field for the photo, and a text area for Ingredients.

    Dashboard Display: Iterate over the recipes in PostgreSQL to display "Recipe Cards." Each card must show:

        The uploaded photo (using the image_path).

        The recipe name.

        A "Push to Todoist" button (triggers a POST request to /api/push/{id} via a standard form submission or fetch API).

        A link/button to view the QR code.

D. Todoist Integration & Concurrency

    Create a single, reusable Go function PushToTodoist(recipeID string) that is called by both the /api/push/ route and the /scan/ route.

    API calls to Todoist should ideally run concurrently using Goroutines (go pushIngredient(item)) so the user isn't waiting on the UI or their phone for multiple HTTP requests to finish.
